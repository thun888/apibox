package siteinfo

// 上游代理规则（modules.siteinfo.proxy）：目标域名命中规则列表中的某条
// 规则时，实际请求改发代理地址。代理地址为模板，其中的 {href} 在请求前
// 替换为目标 URL。
//
//   - domains 支持精确域名（大小写不敏感）与 "*.example.com" 通配，通配
//     同时命中域名本身及其任意子域；"*" 匹配所有域名。端口不参与匹配。
//   - {href} 原样替换（不额外编码），适合 cors-anywhere 风格的前缀代理
//     （如 https://aaa.com/{href}）；多个 {href} 均被替换。
//   - 命中代理的请求不再解析/校验目标主机（连接由代理服务发起，代理为
//     运营者配置的可信设施），但代理地址自身仍需通过 SSRF 校验（scheme
//     限 http/https、主机须为公网地址）；重定向目标命中规则时同样改写为
//     代理地址，未命中时照常直连并校验。

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/net/idna"

	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/utils"
)

// unsafeHost 目标/代理主机的 SSRF 校验；测试中可替换。
var unsafeHost = utils.IsUnsafeHost

// proxyRule 编译后的一条代理规则。
type proxyRule struct {
	template string
	exact    map[string]struct{} // 规范化后的精确域名
	suffixes []string            // 通配后缀；"" 表示匹配全部（裸 "*"）
}

// match 判断规范化后的 host 是否命中：exact 优先，其次按配置顺序的 wildcard。
func (r proxyRule) match(host string) bool {
	if _, ok := r.exact[host]; ok {
		return true
	}
	for _, suf := range r.suffixes {
		if suf == "" || host == suf || strings.HasSuffix(host, "."+suf) {
			return true
		}
	}
	return false
}

// compileProxyRules 编译代理规则；空模板或没有有效域名的规则被跳过，
// 不支持的 "*" 位置记警告后忽略该条目。
func compileProxyRules(cfgs []config.SiteInfoProxyConfig) []proxyRule {
	rules := make([]proxyRule, 0, len(cfgs))
	for _, c := range cfgs {
		if strings.TrimSpace(c.Template) == "" {
			log.Warn("proxy rule skipped: empty template")
			continue
		}
		r := proxyRule{template: c.Template, exact: make(map[string]struct{})}
		for _, d := range c.Domains {
			d = strings.TrimSpace(d)
			switch {
			case d == "":
			case d == "*":
				r.suffixes = append(r.suffixes, "")
			case strings.HasPrefix(d, "*."):
				if suf := normalizeDomain(strings.TrimPrefix(d, "*.")); suf != "" {
					r.suffixes = append(r.suffixes, suf)
				}
			case strings.Contains(d, "*"):
				log.Warn("unsupported wildcard pattern ignored", "domain", d)
			default:
				r.exact[normalizeDomain(d)] = struct{}{}
			}
		}
		if len(r.exact) == 0 && len(r.suffixes) == 0 {
			continue // 无有效域名，规则不生效
		}
		rules = append(rules, r)
	}
	return rules
}

// normalizeDomain 域名规范化：去空白与末尾点、小写、非 ASCII 时做 IDN
// 转 ASCII。
func normalizeDomain(host string) string {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	host = strings.ToLower(host)
	if !isASCII(host) {
		if ascii, err := idna.Lookup.ToASCII(host); err == nil {
			host = ascii
		}
	}
	return host
}

// isASCII 判断字符串是否纯 ASCII：纯 ASCII 域名无需 IDN 转换，直接小写
// 即可，省去 punycode 处理。
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// proxyRulesCache 编译结果缓存：生产环境 config.Cfg 载入后不再变化，
// 按配置指针缓存编译结果（首次使用时编译一次）；配置指针变化（如测试
// 替换 config.Cfg）时自动重编译，无需显式失效。配置对象载入后不应被
// 原地修改（本应用亦无热更配置的需求）。
var proxyRulesCache struct {
	sync.Mutex
	src   *config.Config
	rules []proxyRule
}

// compiledProxyRules 返回当前配置的编译后代理规则；config.Cfg 为 nil
// 时返回 nil（视为未配置代理）。
func compiledProxyRules() []proxyRule {
	cfg := config.Cfg
	proxyRulesCache.Lock()
	defer proxyRulesCache.Unlock()
	if cfg == proxyRulesCache.src {
		return proxyRulesCache.rules
	}
	proxyRulesCache.src = cfg
	proxyRulesCache.rules = nil
	if cfg != nil {
		proxyRulesCache.rules = compileProxyRules(cfg.Modules.SiteInfo.Proxy)
	}
	return proxyRulesCache.rules
}

// proxyTemplateFor 返回 host 命中的第一条规则模板；未命中返回 false。
func proxyTemplateFor(host string) (string, bool) {
	rules := compiledProxyRules()
	if len(rules) == 0 {
		return "", false // 未配置代理：直接短路，跳过域名规范化
	}
	host = normalizeDomain(host)
	for _, r := range rules {
		if r.match(host) {
			return r.template, true
		}
	}
	return "", false
}

// resolveProxy 计算目标的实际请求地址：host 命中代理规则时，把模板中的
// {href} 替换为目标 URL（剔除 fragment）后作为请求地址，否则原样返回
// 目标。viaProxy 表示是否命中代理；err 仅在命中代理但模板无效或代理
// 地址未通过 SSRF 校验时非空。
func resolveProxy(ctx context.Context, target *url.URL) (proxied *url.URL, viaProxy bool, err error) {
	tpl, ok := proxyTemplateFor(target.Hostname())
	if !ok {
		return target, false, nil
	}
	href := *target
	href.Fragment = ""
	raw := strings.ReplaceAll(tpl, "{href}", href.String())

	pu, err := url.Parse(raw)
	if err != nil {
		return nil, true, fmt.Errorf("parse proxy url: %w", err)
	}
	if pu.Scheme != "http" && pu.Scheme != "https" {
		return nil, true, fmt.Errorf("proxy url scheme must be http/https: %q", raw)
	}
	if unsafeHost(ctx, pu.Hostname()) {
		return nil, true, fmt.Errorf("%w: proxy host %s", utils.ErrUnsafeHost, pu.Hostname())
	}
	return pu, true, nil
}

// iconRedirectURL 图标超限 302 的跳转地址：图标域名命中代理规则时改写为
// 代理地址，保证客户端同样经由代理取图；未命中或改写失败时原样返回。
func iconRedirectURL(ctx context.Context, iconURL string) string {
	u, err := url.Parse(iconURL)
	if err != nil {
		return iconURL
	}
	pu, viaProxy, err := resolveProxy(ctx, u)
	if err != nil || !viaProxy {
		return iconURL
	}
	return pu.String()
}
