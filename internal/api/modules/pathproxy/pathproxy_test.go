package pathproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/thun888/apibox/internal/config"
)

func TestCompileRulesMatches(t *testing.T) {
	rules := compileRules([]config.PathProxyRuleConfig{
		{Path: "/api1/users", Target: "https://api.example.com/users", AllowedReferers: []string{"localhost:4000"}},
		{Path: "/api2/*", Target: "https://api.example.com/"},
		{Path: "/api3/*", Target: "https://api.example.com/base/"},
		{Path: "/bad*", Target: "https://api.example.com"}, // 不支持的 * 位置，整条忽略
	})
	if len(rules) != 3 {
		t.Fatalf("len(rules) = %d, want 3", len(rules))
	}
	if _, ok := rules[0].match("/api1/users"); !ok {
		t.Error("exact rule should match")
	}
	if _, ok := rules[0].match("/api1/users/extra"); ok {
		t.Error("exact rule should not match extra path")
	}
	if rest, ok := rules[1].match("/api2/a/b"); !ok || rest != "/a/b" {
		t.Errorf("wildcard match = (%q, %v), want (/a/b, true)", rest, ok)
	}
	if rest, ok := rules[1].match("/api2"); !ok || rest != "" {
		t.Errorf("wildcard prefix match = (%q, %v), want (\"\", true)", rest, ok)
	}
	if _, ok := rules[1].match("/api2x"); ok {
		t.Error("wildcard should not match /api2x")
	}
	if rest, ok := rules[2].match("/api3"); !ok || rest != "" {
		t.Errorf("prefix-only wildcard match = (%q, %v), want (\"\", true)", rest, ok)
	}
}

func TestJoinURLPath(t *testing.T) {
	tests := []struct {
		base, rest, want string
	}{
		{"", "/users", "/users"},
		{"/", "/users", "/users"},
		{"/base", "/users", "/base/users"},
		{"/base/", "/users", "/base/users"},
		{"/base/", "", "/base/"},
	}
	for _, tt := range tests {
		if got := joinURLPath(tt.base, tt.rest); got != tt.want {
			t.Errorf("joinURLPath(%q, %q) = %q, want %q", tt.base, tt.rest, got, tt.want)
		}
	}
}

func runRequestWithCfg(t *testing.T, cfg *config.Config, method, target string) *httptest.ResponseRecorder {
	return runRequestWithCfgAndReferer(t, cfg, method, target, "http://localhost:4000/")
}

func runRequestWithCfgAndReferer(t *testing.T, cfg *config.Config, method, target, referer string) *httptest.ResponseRecorder {
	t.Helper()
	old := config.Cfg
	config.Cfg = cfg
	defer func() { config.Cfg = old }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	ctrl := &Controller{}
	g := r.Group("/api/" + ctrl.ModuleName())
	ctrl.Register(g)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Referer", referer)
	r.ServeHTTP(w, req)
	return w
}

func TestExactRuleProxies(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authentication")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	enable := true
	cfg := &config.Config{
		Modules: config.ModulesConfig{
			PathProxy: config.PathProxyConfig{
				Enable: &enable,
				PathRules: []config.PathProxyRuleConfig{
					{Path: "/api1/users", Target: upstream.URL + "/users", AllowedReferers: []string{"localhost:4000"}, Headers: map[string]string{"Authentication": "Bearer tok"}},
				},
			},
		},
	}
	w := runRequestWithCfg(t, cfg, http.MethodGet, "/api/pathproxy/api1/users?x=1")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if gotPath != "/users" {
		t.Errorf("upstream path = %q, want /users", gotPath)
	}
	if gotQuery != "x=1" {
		t.Errorf("upstream query = %q, want x=1", gotQuery)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("upstream auth = %q, want Bearer tok", gotAuth)
	}
}

func TestWildcardRuleProxies(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	enable := true
	cfg := &config.Config{
		Modules: config.ModulesConfig{
			PathProxy: config.PathProxyConfig{
				Enable: &enable,
				PathRules: []config.PathProxyRuleConfig{
					{Path: "/api2/*", Target: upstream.URL + "/base/", AllowedReferers: []string{"localhost:4000"}},
				},
			},
		},
	}
	w := runRequestWithCfg(t, cfg, http.MethodGet, "/api/pathproxy/api2/users/42")
	if w.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", w.Code)
	}
	if gotPath != "/base/users/42" {
		t.Errorf("upstream path = %q, want /base/users/42", gotPath)
	}
}

func TestNoMatchReturns404(t *testing.T) {
	enable := true
	cfg := &config.Config{
		Modules: config.ModulesConfig{
			PathProxy: config.PathProxyConfig{
				Enable: &enable,
				PathRules: []config.PathProxyRuleConfig{
					{Path: "/api1/users", Target: "https://api.example.com/users", AllowedReferers: []string{"localhost:4000"}},
				},
			},
		},
	}
	w := runRequestWithCfg(t, cfg, http.MethodGet, "/api/pathproxy/other")
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", w.Code)
	}
}

func TestRefererForbidden(t *testing.T) {
	enable := true
	cfg := &config.Config{
		Modules: config.ModulesConfig{
			PathProxy: config.PathProxyConfig{
				Enable: &enable,
				PathRules: []config.PathProxyRuleConfig{
					{Path: "/api1/users", Target: "https://api.example.com/users", AllowedReferers: []string{"localhost:4000"}},
				},
			},
		},
	}
	old := config.Cfg
	config.Cfg = cfg
	defer func() { config.Cfg = old }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	ctrl := &Controller{}
	g := r.Group("/api/" + ctrl.ModuleName())
	ctrl.Register(g)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pathproxy/api1/users", nil)
	req.Header.Set("Referer", "http://evil.example/")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", w.Code)
	}
}

func TestPerRuleReferersIndependent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	enable := true
	cfg := &config.Config{
		Modules: config.ModulesConfig{
			PathProxy: config.PathProxyConfig{
				Enable: &enable,
				PathRules: []config.PathProxyRuleConfig{
					{Path: "/api1/*", Target: upstream.URL + "/", AllowedReferers: []string{"localhost:4000"}},
					{Path: "/api2/*", Target: upstream.URL + "/", AllowedReferers: []string{"localhost:5000"}},
				},
			},
		},
	}

	if w := runRequestWithCfgAndReferer(t, cfg, http.MethodGet, "/api/pathproxy/api1/users", "http://localhost:5000/"); w.Code != http.StatusForbidden {
		t.Errorf("rule1 with mismatched referer code = %d, want 403", w.Code)
	}
	if w := runRequestWithCfgAndReferer(t, cfg, http.MethodGet, "/api/pathproxy/api1/users", "http://localhost:4000/"); w.Code != http.StatusNoContent {
		t.Errorf("rule1 with matched referer code = %d, want 204", w.Code)
	}
	if w := runRequestWithCfgAndReferer(t, cfg, http.MethodGet, "/api/pathproxy/api2/users", "http://localhost:4000/"); w.Code != http.StatusForbidden {
		t.Errorf("rule2 with mismatched referer code = %d, want 403", w.Code)
	}
	if w := runRequestWithCfgAndReferer(t, cfg, http.MethodGet, "/api/pathproxy/api2/users", "http://localhost:5000/"); w.Code != http.StatusNoContent {
		t.Errorf("rule2 with matched referer code = %d, want 204", w.Code)
	}
}

func TestRuleWithoutReferersForbidden(t *testing.T) {
	var hit bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	enable := true
	cfg := &config.Config{
		Modules: config.ModulesConfig{
			PathProxy: config.PathProxyConfig{
				Enable: &enable,
				PathRules: []config.PathProxyRuleConfig{
					{Path: "/api1/*", Target: upstream.URL + "/"},
				},
			},
		},
	}

	w := runRequestWithCfgAndReferer(t, cfg, http.MethodGet, "/api/pathproxy/api1/users", "http://localhost:4000/")
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", w.Code)
	}
	if hit {
		t.Fatal("upstream should not have been hit without rule-level allowed_referers")
	}
}

func TestWildcardRulePreservesEscapedPath(t *testing.T) {
	var gotEscaped string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscaped = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	enable := true
	cfg := &config.Config{
		Modules: config.ModulesConfig{
			PathProxy: config.PathProxyConfig{
				Enable: &enable,
				PathRules: []config.PathProxyRuleConfig{
					{Path: "/api2/*", Target: upstream.URL + "/base/", AllowedReferers: []string{"localhost:4000"}},
				},
			},
		},
	}
	w := runRequestWithCfg(t, cfg, http.MethodGet, "/api/pathproxy/api2/a%2Fb")
	if w.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", w.Code)
	}
	if gotEscaped != "/base/a%2Fb" {
		t.Errorf("upstream escaped path = %q, want /base/a%%2Fb", gotEscaped)
	}
}

func TestPathProxyStripsUpstreamCORSHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	enable := true
	cfg := &config.Config{
		Modules: config.ModulesConfig{
			PathProxy: config.PathProxyConfig{
				Enable: &enable,
				PathRules: []config.PathProxyRuleConfig{
					{Path: "/raw/*", Target: upstream.URL + "/", AllowedReferers: []string{"*"}},
				},
			},
		},
	}

	old := config.Cfg
	config.Cfg = cfg
	defer func() { config.Cfg = old }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟全局 CORS 中间件已写入本服务自己的 CORS 响应头
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "https://blog.hzchu.top")
		c.Next()
	})
	ctrl := &Controller{}
	g := r.Group("/api/" + ctrl.ModuleName())
	ctrl.Register(g)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pathproxy/raw/thun888/Friend-Circle/file.json", nil)
	req.Header.Set("Referer", "https://blog.hzchu.top/")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	values := w.Header().Values("Access-Control-Allow-Origin")
	if len(values) != 1 || values[0] != "https://blog.hzchu.top" {
		t.Fatalf("Access-Control-Allow-Origin = %v, want exactly [https://blog.hzchu.top]", values)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("upstream Access-Control-Allow-Credentials should be stripped, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("upstream Access-Control-Allow-Methods should be stripped, got %q", got)
	}
}
