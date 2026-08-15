package utils

import (
	"context"
	"errors"
	"testing"
)

func TestIsUnsafeHostBlockedLiterals(t *testing.T) {
	blocked := []string{
		// IPv4：环回、私网、链路本地（云元数据）、CGNAT（阿里云元数据）、保留段
		"127.0.0.1", "127.8.8.8",
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "169.254.1.1",
		"100.64.0.1", "100.100.100.200",
		"0.0.0.0", "0.1.2.3",
		"192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1",
		"224.0.0.1", "239.255.255.255", "240.0.0.1", "255.255.255.255",
		// IPv6：环回、未指定、链路本地、ULA、组播、文档、隧道（内嵌 IPv4）
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1",
		"2001:db8::1", "2001::1", "2002:0101:0101::1", "64:ff9b::a00:1",
		// IPv4-mapped IPv6 按内嵌 IPv4 判断
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
	}
	for _, host := range blocked {
		t.Run(host, func(t *testing.T) {
			if !IsUnsafeHost(context.Background(), host) {
				t.Errorf("IsUnsafeHost(%q) = false, want true", host)
			}
		})
	}
}

func TestIsUnsafeHostAllowedLiterals(t *testing.T) {
	allowed := []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34",
		"2001:4860:4860::8888",
		"::ffff:8.8.8.8", // mapped 公网地址不拦截
	}
	for _, host := range allowed {
		t.Run(host, func(t *testing.T) {
			if IsUnsafeHost(context.Background(), host) {
				t.Errorf("IsUnsafeHost(%q) = true, want false", host)
			}
		})
	}
}

// localhost 正常解析到环回地址；即便在无解析环境也会 fail-closed，
// 两种情况都应拦截。
func TestIsUnsafeHostLocalhost(t *testing.T) {
	if !IsUnsafeHost(context.Background(), "localhost") {
		t.Fatal("IsUnsafeHost(localhost) = false, want true")
	}
}

func TestResolveSafeHostBlockedReturnsErrUnsafeHost(t *testing.T) {
	_, err := resolveSafeHost(context.Background(), "169.254.169.254")
	if !errors.Is(err, ErrUnsafeHost) {
		t.Fatalf("err = %v, want ErrUnsafeHost", err)
	}
}
