package siteinfo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/utils"
)

// setProxyCfg 替换全局代理配置，返回恢复函数。
func setProxyCfg(rules ...config.SiteInfoProxyConfig) func() {
	old := config.Cfg
	config.Cfg = &config.Config{
		Modules: config.ModulesConfig{
			SiteInfo: config.SiteInfoConfig{Proxy: rules},
		},
	}
	return func() { config.Cfg = old }
}

func TestProxyTemplateFor(t *testing.T) {
	defer setProxyCfg(config.SiteInfoProxyConfig{
		Template: "https://p1/{href}",
		Domains:  []string{"Example.COM", "*.foo.example.com"},
	})()

	tests := []struct {
		host    string
		want    string
		matched bool
	}{
		{"example.com", "https://p1/{href}", true},         // 精确，大小写不敏感
		{"EXAMPLE.com", "https://p1/{href}", true},         // 精确，大小写不敏感
		{"sub.example.com", "", false},                     // 精确不命中子域
		{"foo.example.com", "https://p1/{href}", true},     // 通配命中域名本身
		{"a.foo.example.com", "https://p1/{href}", true},   // 通配命中子域
		{"a.b.foo.example.com", "https://p1/{href}", true}, /* 通配命中多级子域 */
		{"FOO.EXAMPLE.COM", "https://p1/{href}", true},     // 通配大小写不敏感
		{"xfoo.example.com", "", false},                    // 后缀相似但不以 ".foo.example.com" 结尾
		{"notexample.com", "", false},
		{"other.org", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got, ok := proxyTemplateFor(tt.host)
			if ok != tt.matched || (ok && got != tt.want) {
				t.Errorf("proxyTemplateFor(%q) = (%q, %v), want (%q, %v)",
					tt.host, got, ok, tt.want, tt.matched)
			}
		})
	}
}

// 多规则：先匹配者生效；无效通配（* 不在开头）与空模板的规则整条忽略。
func TestProxyTemplateForPrecedence(t *testing.T) {
	defer setProxyCfg(
		config.SiteInfoProxyConfig{Template: "https://p1/{href}", Domains: []string{"example.com"}},
		config.SiteInfoProxyConfig{Template: "https://p2/{href}", Domains: []string{"*.example.com"}},
		config.SiteInfoProxyConfig{Template: "https://p3/{href}", Domains: []string{"a*b.com"}},
		config.SiteInfoProxyConfig{Template: "", Domains: []string{"example.com"}},
	)()

	tests := []struct {
		host    string
		want    string
		matched bool
	}{
		{"example.com", "https://p1/{href}", true},     // 第一条规则先命中
		{"sub.example.com", "https://p2/{href}", true}, // 第二条通配
		{"axb.com", "", false},                         // 无效通配规则被忽略
		{"other.org", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got, ok := proxyTemplateFor(tt.host)
			if ok != tt.matched || (ok && got != tt.want) {
				t.Errorf("proxyTemplateFor(%q) = (%q, %v), want (%q, %v)",
					tt.host, got, ok, tt.want, tt.matched)
			}
		})
	}
}

func TestProxyTemplateForBareStar(t *testing.T) {
	defer setProxyCfg(config.SiteInfoProxyConfig{
		Template: "https://p/{href}",
		Domains:  []string{"*"},
	})()

	if got, ok := proxyTemplateFor("any.host.example"); !ok || got != "https://p/{href}" {
		t.Errorf("bare * should match all hosts, got (%q, %v)", got, ok)
	}
}

func TestResolveProxy(t *testing.T) {
	ctx := context.Background()

	t.Run("matched substitutes href and strips fragment", func(t *testing.T) {
		defer setProxyCfg(config.SiteInfoProxyConfig{
			Template: "https://8.8.8.8/{href}",
			Domains:  []string{"*.example.com"},
		})()
		pu, viaProxy, err := resolveProxy(ctx, mustURL(t, "https://a.example.com/x?y=1#frag"))
		if err != nil {
			t.Fatal(err)
		}
		if !viaProxy {
			t.Fatal("viaProxy = false, want true")
		}
		want := "https://8.8.8.8/https://a.example.com/x?y=1"
		if pu.String() != want {
			t.Errorf("proxied = %q, want %q", pu, want)
		}
	})

	t.Run("not matched returns original", func(t *testing.T) {
		defer setProxyCfg(config.SiteInfoProxyConfig{
			Template: "https://8.8.8.8/{href}",
			Domains:  []string{"*.example.com"},
		})()
		u := mustURL(t, "https://other.org/x")
		pu, viaProxy, err := resolveProxy(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		if viaProxy {
			t.Fatal("viaProxy = true, want false")
		}
		if pu.String() != u.String() {
			t.Errorf("proxied = %q, want original %q", pu, u)
		}
	})

	t.Run("unsafe proxy host rejected", func(t *testing.T) {
		defer setProxyCfg(config.SiteInfoProxyConfig{
			Template: "http://127.0.0.1/{href}",
			Domains:  []string{"*.example.com"},
		})()
		_, _, err := resolveProxy(ctx, mustURL(t, "https://a.example.com/"))
		if !errors.Is(err, utils.ErrUnsafeHost) {
			t.Fatalf("err = %v, want ErrUnsafeHost", err)
		}
	})

	t.Run("non-http proxy scheme rejected", func(t *testing.T) {
		defer setProxyCfg(config.SiteInfoProxyConfig{
			Template: "ftp://8.8.8.8/{href}",
			Domains:  []string{"*.example.com"},
		})()
		_, _, err := resolveProxy(ctx, mustURL(t, "https://a.example.com/"))
		if err == nil {
			t.Fatal("ftp proxy template should fail")
		}
		if errors.Is(err, utils.ErrUnsafeHost) {
			t.Fatalf("err = %v, want template error", err)
		}
	})

	t.Run("invalid template rejected", func(t *testing.T) {
		defer setProxyCfg(config.SiteInfoProxyConfig{
			Template: "://bad",
			Domains:  []string{"*.example.com"},
		})()
		_, _, err := resolveProxy(ctx, mustURL(t, "https://a.example.com/"))
		if err == nil {
			t.Fatal("invalid template should fail")
		}
	})
}

// 命中代理的页面抓取：请求改发代理地址，重定向目标同样改写；
// 页面最终 URL 从上下文还原，作为相对图标路径的解析基准。
func TestFetchHTMLThroughProxy(t *testing.T) {
	oldCfg := config.Cfg
	oldUnsafe := unsafeHost
	defer func() {
		config.Cfg = oldCfg
		unsafeHost = oldUnsafe
	}()
	// 测试代理监听在环回地址，放行代理主机校验
	unsafeHost = func(context.Context, string) bool { return false }

	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		if r.URL.Path == "/p/https://a.example.com/page" {
			http.Redirect(w, r, "https://b.example.com/final", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, `<html><head><title>T</title><link rel="icon" href="favicon.ico"></head></html>`)
	}))
	defer srv.Close()

	config.Cfg = &config.Config{
		Modules: config.ModulesConfig{
			SiteInfo: config.SiteInfoConfig{
				Proxy: []config.SiteInfoProxyConfig{
					{Template: srv.URL + "/p/{href}", Domains: []string{"*.example.com"}},
				},
			},
		},
	}

	htmlStr, base, err := fetchHTML(context.Background(), mustURL(t, "https://a.example.com/page"))
	if err != nil {
		t.Fatal(err)
	}

	wantPaths := []string{"/p/https://a.example.com/page", "/p/https://b.example.com/final"}
	if !reflect.DeepEqual(got, wantPaths) {
		t.Errorf("proxy requests = %v, want %v", got, wantPaths)
	}
	if base.String() != "https://b.example.com/final" {
		t.Errorf("base = %q, want final page URL", base)
	}
	_, _, icon := parseInfo(htmlStr, base)
	if icon != "https://b.example.com/favicon.ico" {
		t.Errorf("icon = %q, want resolved against final page URL", icon)
	}
}

// 命中代理的图标抓取应请求代理地址。
func TestFetchIconUsesProxy(t *testing.T) {
	defer setProxyCfg(config.SiteInfoProxyConfig{
		Template: "https://8.8.8.8/{href}",
		Domains:  []string{"*.example.com"},
	})()

	old := httpClient
	defer func() { httpClient = old }()

	var got string
	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			got = req.URL.String()
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Header:        http.Header{},
				Body:          io.NopCloser(bytes.NewReader(nil)),
				ContentLength: 0,
				Request:       req,
			}, nil
		}),
	}

	resp, err := fetchIcon(context.Background(), "http://a.example.com/i.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	want := "https://8.8.8.8/http://a.example.com/i.png"
	if got != want {
		t.Errorf("icon request = %q, want %q", got, want)
	}
}

// 图标超限 302 时，跳转地址同样改写为代理地址。
func TestServeIconRedirectsThroughProxy(t *testing.T) {
	defer setProxyCfg(config.SiteInfoProxyConfig{
		Template: "https://8.8.8.8/{href}",
		Domains:  []string{"*.example.com"},
	})()

	old := httpClient
	defer func() { httpClient = old }()

	var got string
	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			got = req.URL.String()
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
	icon := "http://cdn.example.com/huge.png"
	serveIcon(c, icon)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", w.Code)
	}
	want := "https://8.8.8.8/http://cdn.example.com/huge.png"
	if loc := w.Header().Get("Location"); loc != want {
		t.Errorf("location = %q, want %q", loc, want)
	}
	if got != want {
		t.Errorf("fetch request = %q, want %q", got, want)
	}
}

func TestIconRedirectURL(t *testing.T) {
	defer setProxyCfg(config.SiteInfoProxyConfig{
		Template: "https://8.8.8.8/{href}",
		Domains:  []string{"*.example.com"},
	})()

	ctx := context.Background()
	if got := iconRedirectURL(ctx, "http://a.example.com/i.png"); got != "https://8.8.8.8/http://a.example.com/i.png" {
		t.Errorf("proxied redirect = %q", got)
	}
	if got := iconRedirectURL(ctx, "http://other.org/i.png"); got != "http://other.org/i.png" {
		t.Errorf("non-proxied redirect = %q, want unchanged", got)
	}
	if got := iconRedirectURL(ctx, "::bad"); got != "::bad" {
		t.Errorf("invalid url redirect = %q, want unchanged", got)
	}
}

// 热路径：规则已编译缓存，测量单次域名匹配开销。
func BenchmarkProxyTemplateForCached(b *testing.B) {
	defer setProxyCfg(
		config.SiteInfoProxyConfig{Template: "https://p1/{href}", Domains: []string{"Example.COM", "*.foo.example.com"}},
		config.SiteInfoProxyConfig{Template: "https://p2/{href}", Domains: []string{"*.bar.example.net"}},
	)()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := proxyTemplateFor("a.foo.example.com"); !ok {
			b.Fatal("expected match")
		}
	}
}

// 未配置代理的短路路径：应无分配且最快。
func BenchmarkProxyTemplateForNoRules(b *testing.B) {
	defer setProxyCfg()()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := proxyTemplateFor("a.foo.example.com"); ok {
			b.Fatal("unexpected match")
		}
	}
}

// 每次替换配置指针强制重编译（旧实现的开销；新实现仅在配置变化时发生）。
func BenchmarkProxyTemplateForRecompile(b *testing.B) {
	defer setProxyCfg()()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		config.Cfg = &config.Config{Modules: config.ModulesConfig{
			SiteInfo: config.SiteInfoConfig{Proxy: []config.SiteInfoProxyConfig{
				{Template: "https://p1/{href}", Domains: []string{"Example.COM", "*.foo.example.com"}},
			}},
		}}
		if _, ok := proxyTemplateFor("a.foo.example.com"); !ok {
			b.Fatal("expected match")
		}
	}
}
