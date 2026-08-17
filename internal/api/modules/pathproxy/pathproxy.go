// Package pathproxy 路径代理模块：把 /api/pathproxy/<path> 的请求按配置
// 规则转发到对应上游 URL，支持精确路径与末尾 "*" 通配。
//
// 规则在 modules.pathproxy.path_rules 中配置：
//
//   - path: "/api1/users"
//     target: "https://api.example.com/users"
//     allowed_referers:
//   - "localhost:4000"
//     headers:
//     Authentication: "Bearer your-token"
//   - path: "/api2/*"
//     target: "https://api.example.com/"
//     allowed_referers:
//   - "localhost:5000"
//
// path 为模块前缀 /api/pathproxy 之后的路径；末尾 "*" 表示通配其后任意
// 路径，命中后把通配部分拼接在 target 的路径之后。请求方法、查询参数与
// 请求体原样转发；headers 中的配置会设置/覆盖到上游请求。每条规则通过
// 自己的 allowed_referers 校验来源。
package pathproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/utils"
)

const (
	moduleName      = "pathproxy"
	upstreamTimeout = 30 * time.Second
)

var log = utils.NewModuleLogger(moduleName)

// Controller 路径代理模块控制器。
type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

// ModuleName 模块名，即路由前缀。
func (c *Controller) ModuleName() string { return moduleName }

// Enabled 模块是否启用。
func (c *Controller) Enabled() bool { return config.Cfg.Modules.PathProxy.Enabled() }

// Register 注册路由：/api/pathproxy/*proxyPath，支持任意 HTTP 方法。
func (c *Controller) Register(r *gin.RouterGroup) {
	r.Any("/*proxyPath", c.handle)
}

// proxyRule 编译后的一条路径代理规则。
type proxyRule struct {
	path            string // 配置的 path，如 /api1/users
	prefix          string // 精确路径，或通配规则中去掉 "/*" 的前缀
	wildcard        bool   // 是否为末尾 "*" 通配
	target          *url.URL
	allowedReferers []string
	headers         map[string]string
}

// match 判断模块前缀之后的请求路径 p 是否命中当前规则；命中时 rest 为
// 通配部分（以 "/" 开头），精确规则 rest 恒为空。
func (r proxyRule) match(p string) (rest string, ok bool) {
	if !r.wildcard {
		return "", p == r.prefix
	}
	if r.prefix == "" {
		// 规则 path 为 "/*" 时匹配任意路径。
		return p, true
	}
	if p == r.prefix {
		return "", true
	}
	if strings.HasPrefix(p, r.prefix+"/") {
		return p[len(r.prefix):], true
	}
	return "", false
}

// compileRules 编译路径代理规则；path/target 为空、path 不以 "/" 开头、
// target 非 http/https 或带不支持的 "*" 位置的规则整条跳过。
func compileRules(cfgs []config.PathProxyRuleConfig) []proxyRule {
	rules := make([]proxyRule, 0, len(cfgs))
	for _, c := range cfgs {
		path := strings.TrimSpace(c.Path)
		targetRaw := strings.TrimSpace(c.Target)
		if path == "" || targetRaw == "" {
			log.Warn("path proxy rule skipped: empty path or target")
			continue
		}
		if !strings.HasPrefix(path, "/") {
			log.Warn("path proxy rule skipped: path must start with /", "path", path)
			continue
		}

		target, err := url.Parse(targetRaw)
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
			log.Warn("path proxy rule skipped: invalid target", "target", targetRaw, "error", err)
			continue
		}

		r := proxyRule{
			path:            path,
			target:          target,
			allowedReferers: c.AllowedReferers,
			headers:         make(map[string]string),
		}
		switch {
		case path == "/*":
			r.wildcard = true
			r.prefix = ""
		case strings.HasSuffix(path, "/*"):
			r.wildcard = true
			r.prefix = strings.TrimSuffix(path, "/*")
		case strings.Contains(path, "*"):
			log.Warn("path proxy rule skipped: unsupported wildcard position", "path", path)
			continue
		default:
			r.prefix = path
		}

		for k, v := range c.Headers {
			r.headers[k] = v
		}
		rules = append(rules, r)
	}
	return rules
}

// rulesCache 按 config.Cfg 指针缓存编译结果；配置对象载入后不变，测试中
// 替换指针时自动重编译。
var rulesCache struct {
	sync.Mutex
	src   *config.Config
	rules []proxyRule
}

func compiledRules() []proxyRule {
	cfg := config.Cfg
	rulesCache.Lock()
	defer rulesCache.Unlock()
	if cfg == rulesCache.src {
		return rulesCache.rules
	}
	rulesCache.src = cfg
	rulesCache.rules = nil
	if cfg != nil {
		rulesCache.rules = compileRules(cfg.Modules.PathProxy.PathRules)
	}
	return rulesCache.rules
}

// upstreamTransport 代理上游的传输层：禁用环境变量代理，设置各阶段超时。
var upstreamTransport = &http.Transport{
	Proxy:                 nil,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ResponseHeaderTimeout: upstreamTimeout,
}

// handle 按命中的规则转发请求：先匹配路径，再按该规则单独校验 Referer。
func (c *Controller) handle(ctx *gin.Context) {
	p := ctx.Param("proxyPath")
	if p == "" {
		p = "/"
	}

	rules := compiledRules()
	var matched *proxyRule
	var rest string
	for i := range rules {
		if r, ok := rules[i].match(p); ok {
			matched = &rules[i]
			rest = r
			break
		}
	}
	if matched == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "no matching path rule"})
		return
	}

	if !utils.CheckReferer(matched.allowedReferers, ctx) {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	target := *matched.target
	target.RawQuery = mergeQuery(target.RawQuery, ctx.Request.URL.RawQuery)
	target.Fragment = ""
	if matched.wildcard {
		rawRest := rawRestPath(ctx.Request, matched.prefix, rest)
		oldRawPath := target.EscapedPath()
		target.Path = joinURLPath(target.Path, rest)
		if rest == "" {
			target.RawPath = oldRawPath
		} else if rawRest != "" {
			target.RawPath = joinURLPath(oldRawPath, rawRest)
		}
	}

	// 统一设置上游超时；请求结束（或超时）时释放。
	reqCtx, cancel := context.WithTimeout(ctx.Request.Context(), upstreamTimeout)
	defer cancel()
	ctx.Request = ctx.Request.WithContext(reqCtx)

	proxy := &httputil.ReverseProxy{
		ModifyResponse: stripUpstreamCORSHeaders,
		Rewrite: func(pr *httputil.ProxyRequest) {
			// 目标 URL 已经包含最终路径，不能使用 SetURL（它会再次拼接
			// 原始请求路径），这里直接覆盖 outbound URL 各字段。
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.URL.Path = target.Path
			pr.Out.URL.RawPath = target.RawPath
			pr.Out.URL.RawQuery = target.RawQuery
			pr.Out.URL.Fragment = ""
			pr.Out.URL.User = target.User
			pr.Out.Host = target.Host
			pr.SetXForwarded()
			for k, v := range matched.headers {
				if strings.EqualFold(k, "Host") {
					pr.Out.Host = v
					continue
				}
				pr.Out.Header.Set(k, v)
			}
		},
		Transport:    upstreamTransport,
		ErrorHandler: pathProxyErrorHandler,
	}

	proxy.ServeHTTP(ctx.Writer, ctx.Request)
}

func pathProxyErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	log.Error("upstream proxy failed", "error", err, "url", r.URL.String())
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(`{"error":"upstream error"}`))
}

// stripUpstreamCORSHeaders 删除上游响应中的 CORS 头。全局 CORS 中间件
// 已经按本服务配置写好 CORS 响应头；如果上游也返回 CORS 头（如
// raw.githubusercontent.com 返回 Access-Control-Allow-Origin: *），
// ReverseProxy 会叠加成多个值，浏览器会拒绝该响应。
func stripUpstreamCORSHeaders(resp *http.Response) error {
	for _, key := range corsResponseHeaders {
		resp.Header.Del(key)
	}
	return nil
}

var corsResponseHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Credentials",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
	"Access-Control-Expose-Headers",
	"Access-Control-Max-Age",
	"Access-Control-Allow-Private-Network",
}

// mergeQuery 合并 target 自带 query 与请求 query；均为空时返回空串。
func mergeQuery(targetQuery, reqQuery string) string {
	switch {
	case targetQuery == "":
		return reqQuery
	case reqQuery == "":
		return targetQuery
	default:
		return targetQuery + "&" + reqQuery
	}
}

// joinURLPath 把 rest（以 "/" 开头）拼接到 base 路径之后，避免重复或缺失
// 斜杠。rest 为空时返回 base 原样。
func joinURLPath(base, rest string) string {
	if rest == "" {
		return base
	}
	if base == "" || base == "/" {
		return rest
	}
	if strings.HasSuffix(base, "/") {
		return base[:len(base)-1] + rest
	}
	return base + rest
}

// rawRestPath 返回请求路径中通配部分的原始转义形式。优先从请求 URL 的
// EscapedPath 中按通配前缀截取，以保留 %2F 等编码；计算失败时回退为已
// 解码的 rest。
func rawRestPath(req *http.Request, prefix, rest string) string {
	if rest == "" {
		return ""
	}
	fullRaw := req.URL.EscapedPath()
	baseRaw := "/api/" + moduleName
	if !strings.HasPrefix(fullRaw, baseRaw) {
		return rest
	}
	reqRaw := strings.TrimPrefix(fullRaw, baseRaw)
	if reqRaw == "" {
		reqRaw = "/"
	}
	prefixRaw := (&url.URL{Path: prefix}).EscapedPath()
	if reqRaw == prefixRaw {
		return ""
	}
	if strings.HasPrefix(reqRaw, prefixRaw+"/") {
		return reqRaw[len(prefixRaw):]
	}
	return rest
}
