package utils

// SSRF 防护：校验主机名/IP 是否解析到内网、环回、链路本地、组播、
// 保留地址段（含云元数据地址 169.254.169.254、阿里云 100.100.100.200 等）。
//
// 校验在发起外连前进行，并对 DNS 的全部解析结果逐一检查（任一命中即
// 拒绝，缓解 DNS 重绑定）；解析失败或超时按拒绝处理（fail-closed）。

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// ErrUnsafeHost 表示目标主机解析到禁止访问的保留地址，已被 SSRF 防护拦截。
var ErrUnsafeHost = errors.New("host resolves to a blocked (internal/loopback/link-local) address")

// resolveTimeout 单次 DNS 解析上限。
const resolveTimeout = 5 * time.Second

// blockedPrefixes 为 netip 内置 Is* 判断未覆盖的保留地址段。
// 私网/环回/链路本地/组播/未指定地址已由 IsPrivate、IsLoopback、
// IsLinkLocalUnicast、IsLinkLocalMulticast、IsMulticast、IsUnspecified
// 覆盖，不在此重复列出。
var blockedPrefixes = mustPrefixes(
	// IPv4
	"0.0.0.0/8",       // “本网络”
	"100.64.0.0/10",   // CGNAT（含阿里云元数据 100.100.100.200）
	"192.0.0.0/24",    // IETF 协议分配
	"192.0.2.0/24",    // 文档 TEST-NET-1
	"198.18.0.0/15",   // 基准测试
	"198.51.100.0/24", // 文档 TEST-NET-2
	"203.0.113.0/24",  // 文档 TEST-NET-3
	"240.0.0.0/4",     // 保留（含广播 255.255.255.255）
	// IPv6
	"2001:db8::/32", // 文档
	"2001::/32",     // Teredo（内嵌 IPv4）
	"2002::/16",     // 6to4（内嵌 IPv4）
	"64:ff9b::/96",  // NAT64（内嵌 IPv4）
)

func mustPrefixes(raw ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			panic(fmt.Sprintf("invalid blocked prefix %q: %v", s, err))
		}
		out = append(out, p)
	}
	return out
}

// isBlockedAddr 判断单个 IP 是否命中保留地址段。
// netip 的方法对 IPv4-mapped IPv6（::ffff:a.b.c.d）按内嵌 IPv4 判断，
// 因此 ::ffff:127.0.0.1 会被 IsLoopback 命中。
func isBlockedAddr(ip netip.Addr) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	ip = ip.Unmap() // mapped 地址按内嵌 IPv4 匹配前缀表
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// IsUnsafeHost 判断 host（域名或 IP 字面量，不带端口）是否禁止外连。
// 解析出的任一地址命中保留地址段即返回 true；解析失败或超时同样返回
// true（fail-closed，宁可拒绝也不放过）。
func IsUnsafeHost(ctx context.Context, host string) bool {
	_, err := resolveSafeHost(ctx, host)
	return err != nil
}

// resolveSafeHost 解析 host 并校验全部解析结果，任一命中保留地址段即
// 返回错误。返回的 IP 列表可用于“校验后直连”，进一步收窄 DNS 重绑定窗口。
func resolveSafeHost(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "[]") // 兼容带括号的 IPv6 字面量
	if host == "" {
		return nil, fmt.Errorf("%w: empty host", ErrUnsafeHost)
	}

	// IP 字面量直接判断，无需 DNS
	if ip, err := netip.ParseAddr(host); err == nil {
		if isBlockedAddr(ip) {
			return nil, fmt.Errorf("%w: ip literal %s", ErrUnsafeHost, ip)
		}
		return []netip.Addr{ip}, nil
	}

	// 容错：若误传 host:port 形式则剥离端口
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}

	ips := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			return nil, fmt.Errorf("resolve %s: unexpected address %s", host, a.IP)
		}
		if isBlockedAddr(ip) {
			return nil, fmt.Errorf("%w: %s -> %s", ErrUnsafeHost, host, ip)
		}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	return ips, nil
}
