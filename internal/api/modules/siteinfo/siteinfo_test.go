package siteinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/thun888/apibox/internal/utils"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestParseInfo(t *testing.T) {
	base := mustURL(t, "https://example.com/blog/post.html")

	tests := []struct {
		name      string
		html      string
		wantTitle string
		wantDesc  string
		wantIcon  string
	}{
		{
			name:      "title trimmed and shortcut icon resolved against page dir",
			html:      `<html><head><title> Hello World </title><meta property="og:description" content="og desc"><link rel="shortcut icon" href="favicon.ico"></head></html>`,
			wantTitle: "Hello World",
			wantDesc:  "og desc",
			wantIcon:  "https://example.com/blog/favicon.ico",
		},
		{
			name:      "og title description and image fallbacks",
			html:      `<head><meta property="og:title" content="OG Title"><meta name="description" content="meta desc"><meta property="og:image" content="https://cdn.example.com/cover.png"></head>`,
			wantTitle: "OG Title",
			wantDesc:  "meta desc",
			wantIcon:  "https://cdn.example.com/cover.png",
		},
		{
			name:     "og description wins over name description",
			html:     `<head><meta name="description" content="meta desc"><meta property="og:description" content="og desc"></head>`,
			wantDesc: "og desc",
		},
		{
			name:     "apple touch icon wins over icon regardless of order",
			html:     `<head><link rel="icon" href="/i.png"><link rel="apple-touch-icon" href="/apple.png"></head>`,
			wantIcon: "https://example.com/apple.png",
		},
		{
			name:     "twitter image fallback",
			html:     `<head><meta property="twitter:image" content="/tw.png"></head>`,
			wantIcon: "https://example.com/tw.png",
		},
		{
			name:     "data image icon falls back to icon-like link",
			html:     `<head><link rel="icon" href="data:image/png;base64,xx"><link rel="mask-icon" href="/mask.svg"></head>`,
			wantIcon: "https://example.com/mask.svg",
		},
		{
			name:     "parent relative icon resolved",
			html:     `<head><link rel="icon" href="../favicon.ico"></head>`,
			wantIcon: "https://example.com/favicon.ico",
		},
		{
			name:     "javascript icon dropped",
			html:     `<head><link rel="icon" href="javascript:alert(1)"></head>`,
			wantIcon: "",
		},
		{
			name:     "protocol relative icon resolved with base scheme",
			html:     `<head><link rel="icon" href="//cdn.example.com/f.png"></head>`,
			wantIcon: "https://cdn.example.com/f.png",
		},
		{
			name:      "empty title element does not fall back to og:title",
			html:      `<head><title></title><meta property="og:title" content="OG"></head>`,
			wantTitle: "",
		},
		{
			name: "no matches",
			html: `<html><body><p>hello</p></body></html>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, desc, icon := parseInfo(tt.html, base)
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			if desc != tt.wantDesc {
				t.Errorf("desc = %q, want %q", desc, tt.wantDesc)
			}
			if icon != tt.wantIcon {
				t.Errorf("icon = %q, want %q", icon, tt.wantIcon)
			}
		})
	}
}

func TestResolveIcon(t *testing.T) {
	base := mustURL(t, "https://example.com")

	tests := []struct {
		name string
		icon string
		want string
	}{
		{"absolute http", "http://other.com/i.png", "http://other.com/i.png"},
		{"root relative", "/i.png", "https://example.com/i.png"},
		{"protocol relative", "//cdn.example.com/i.png", "https://cdn.example.com/i.png"},
		{"ftp scheme dropped", "ftp://example.com/i.png", ""},
		{"garbage dropped", "::not a url", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveIcon(base, tt.icon); got != tt.want {
				t.Errorf("resolveIcon(%q) = %q, want %q", tt.icon, got, tt.want)
			}
		})
	}
}

// 抓取环回地址应被 SSRF 防护拦截（httptest 监听在 127.0.0.1）。
func TestFetchSiteInfoBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>internal</title></head></html>`))
	}))
	defer srv.Close()

	_, err := fetchSiteInfo(context.Background(), mustURL(t, srv.URL))
	if err == nil {
		t.Fatal("fetchSiteInfo to loopback should fail")
	}
	if !errors.Is(err, utils.ErrUnsafeHost) {
		t.Fatalf("err = %v, want ErrUnsafeHost", err)
	}
}

// 图标指向云元数据地址时应被拦截，且不发起任何网络请求。
func TestFetchIconBlocksMetadata(t *testing.T) {
	_, err := fetchIcon(context.Background(), "http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("fetchIcon to metadata address should fail")
	}
	if !errors.Is(err, utils.ErrUnsafeHost) {
		t.Fatalf("err = %v, want ErrUnsafeHost", err)
	}
}

func TestCheckRedirect(t *testing.T) {
	loopback := mustURL(t, "http://127.0.0.1/admin")
	if err := checkRedirect(&http.Request{URL: loopback}, nil); !errors.Is(err, utils.ErrUnsafeHost) {
		t.Errorf("loopback redirect err = %v, want ErrUnsafeHost", err)
	}

	public := mustURL(t, "https://8.8.8.8/next")
	if err := checkRedirect(&http.Request{URL: public}, nil); err != nil {
		t.Errorf("public redirect err = %v, want nil", err)
	}

	ftp := mustURL(t, "ftp://example.com/file")
	if err := checkRedirect(&http.Request{URL: ftp}, nil); err == nil {
		t.Error("non-http(s) redirect should fail")
	}

	via := make([]*http.Request, 10)
	if err := checkRedirect(&http.Request{URL: public}, via); err == nil {
		t.Error("11th redirect should fail")
	}
}

func TestNormalizeTarget(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty path becomes root", "https://a.com", "https://a.com/"},
		{"scheme and host lowercased", "HTTPS://EXAMPLE.com", "https://example.com/"},
		{"default http port stripped", "http://a.com:80/x", "http://a.com/x"},
		{"default https port stripped", "https://a.com:443/x", "https://a.com/x"},
		{"non-default port kept", "http://a.com:8080/", "http://a.com:8080/"},
		{"fragment dropped", "https://a.com/p#frag", "https://a.com/p"},
		{"query sorted", "https://a.com/?b=2&a=1", "https://a.com/?a=1&b=2"},
		{"dot segments cleaned", "https://a.com/x/../y", "https://a.com/y"},
		{"userinfo stripped", "https://u:p@a.com/", "https://a.com/"},
		{"unicode host to punycode", "https://BÜCHER.de/", "https://xn--bcher-kva.de/"},
		{"ipv6 host bracketed and lowercased", "http://[2001:DB8::1]/", "http://[2001:db8::1]/"},
		{"escaped path detail dropped", "https://a.com/%7e", "https://a.com/~"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := mustURL(t, tt.in)
			if got := normalizeTarget(u); got != tt.want {
				t.Errorf("normalizeTarget(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSiteDataCacheKey(t *testing.T) {
	plain := normalizeTarget(mustURL(t, "https://a.com"))
	slash := normalizeTarget(mustURL(t, "https://a.com/"))
	if siteDataCacheKey(plain, false) != siteDataCacheKey(slash, false) {
		t.Error("equivalent URLs should share cache key")
	}
	if got := siteDataCacheKey("https://a.com/", false); got != "siteinfo:data:https://a.com/" {
		t.Errorf("plain key = %q", got)
	}
	if got := siteDataCacheKey("https://a.com/", true); got != "siteinfo:data64:https://a.com/" {
		t.Errorf("base64 key = %q", got)
	}
	// 命名空间隔离：避免旧格式中 URL 含 "|" 后缀与 base64 变体的 key 碰撞
	if siteDataCacheKey("https://a.com/?x=|true", false) == siteDataCacheKey("https://a.com/?x=", true) {
		t.Error("variants must use distinct namespaces")
	}
}

func TestRewriteCachedURL(t *testing.T) {
	b, ok := rewriteCachedURL(`{"title":"t","url":"https://first.example/"}`, "https://second.example/")
	if !ok {
		t.Fatal("rewrite should succeed")
	}
	var d siteData
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d.URL != "https://second.example/" {
		t.Errorf("url = %q, want rewritten value", d.URL)
	}
	if d.Title != "t" {
		t.Errorf("title = %q, want preserved value", d.Title)
	}

	if _, ok := rewriteCachedURL("{bad json", "https://x/"); ok {
		t.Error("invalid json should fail")
	}
}

// roundTripFunc 把函数适配为 http.RoundTripper，测试中替换 httpClient 用。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// stubIconClient 返回固定响应的 http.Client；ContentLength 置 -1（未知），
// 使 serveIcon 走真实读取路径判断体积。
func stubIconClient(status int, ctype string, body []byte) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			h := http.Header{}
			if ctype != "" {
				h.Set("Content-Type", ctype)
			}
			return &http.Response{
				StatusCode:    status,
				Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
				Header:        h,
				Body:          io.NopCloser(bytes.NewReader(body)),
				ContentLength: -1,
				Request:       req,
			}, nil
		}),
	}
}

func iconTestCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/siteinfo/icon", nil)
	return c, w
}

func TestServeIconForwardsSmallIcon(t *testing.T) {
	old := httpClient
	defer func() { httpClient = old }()
	httpClient = stubIconClient(http.StatusOK, "image/png", []byte{0x89, 'P', 'N', 'G'})

	c, w := iconTestCtx()
	serveIcon(c, "http://8.8.8.8/favicon.png")

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("content-type = %q, want image/png", got)
	}
	if got := w.Header().Get("Content-Length"); got != "4" {
		t.Errorf("content-length = %q, want 4", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=604800" {
		t.Errorf("cache-control = %q", got)
	}
	if !bytes.Equal(w.Body.Bytes(), []byte{0x89, 'P', 'N', 'G'}) {
		t.Errorf("body = %v", w.Body.Bytes())
	}
}

// 体积达到 2MB 即不转发，302 跳转到原图标地址（“只能少于 2MB”）。
func TestServeIconRedirectsAtLimit(t *testing.T) {
	old := httpClient
	defer func() { httpClient = old }()
	httpClient = stubIconClient(http.StatusOK, "image/png", make([]byte, maxIconSize))

	c, w := iconTestCtx()
	icon := "http://8.8.8.8/big.png"
	serveIcon(c, icon)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != icon {
		t.Errorf("location = %q, want %q", got, icon)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=604800" {
		t.Errorf("cache-control = %q", got)
	}
}

// 上游响应已声明超限体积时，不读取 body 直接 302。
func TestServeIconRedirectsDeclaredOversize(t *testing.T) {
	old := httpClient
	defer func() { httpClient = old }()
	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Header:        http.Header{"Content-Type": []string{"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(make([]byte, maxIconSize+1))),
				ContentLength: maxIconSize + 1,
				Request:       req,
			}, nil
		}),
	}

	c, w := iconTestCtx()
	icon := "http://8.8.8.8/huge.png"
	serveIcon(c, icon)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != icon {
		t.Errorf("location = %q, want %q", got, icon)
	}
}

func TestServeIconFallbackContentType(t *testing.T) {
	old := httpClient
	defer func() { httpClient = old }()
	httpClient = stubIconClient(http.StatusOK, "", []byte("ico"))

	c, w := iconTestCtx()
	serveIcon(c, "http://8.8.8.8/favicon.ico")

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "image/x-icon" {
		t.Errorf("content-type = %q, want image/x-icon", got)
	}
}
