package siteinfo

// 上游抓取与 HTML 解析：15s 超时、HTML 限 2MB、自动跟随重定向（上限10 次），解析用 golang.org/x/net/html。
// SSRF 防护：直连目标在抓取前及每次重定向均校验主机，拦截解析到内网/环回/链路本地等保留地址的目标。
// 命中代理规则的域名改经代理请求（见 proxy.go），重定向目标命中时同样改写，此时不再校验目标主机。

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/thun888/apibox/internal/utils"
)

const (
	fetchTimeout = 15 * time.Second
	maxHTMLSize  = 2 << 20 // 2MB
	maxIconSize  = 2 << 20 // 图标体积上限（2MB）：/icon 转发与 base64 转码超限均视为失败
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

var httpClient = &http.Client{
	Timeout:       fetchTimeout,
	CheckRedirect: checkRedirect,
}

// checkRedirect 校验重定向目标（SSRF 防护）：超过 10 跳、非 http(s) 协议、
// 或主机解析到内网/环回/链路本地等保留地址时拒绝跟随。
// 命中代理规则的域名不改直连：记录真实地址（供响应后还原页面最终 URL），
// 并把请求改写为代理地址；代理地址本身的校验由 resolveProxy 完成。
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if s := req.URL.Scheme; s != "http" && s != "https" {
		return fmt.Errorf("redirect to unsupported scheme %q", s)
	}
	pu, viaProxy, err := resolveProxy(req.Context(), req.URL)
	if err != nil {
		return err
	}
	if !viaProxy && unsafeHost(req.Context(), req.URL.Hostname()) {
		return fmt.Errorf("%w: redirect target %s", utils.ErrUnsafeHost, req.URL.Hostname())
	}
	// 无论是否经代理都记录真实地址：直连响应用 resp.Request.URL 即最终
	// URL，代理响应需要它还原页面最终 URL 作为相对链接解析基准。
	ctx := context.WithValue(req.Context(), realURLCtxKey{}, req.URL.String())
	*req = *req.WithContext(ctx)
	if viaProxy {
		req.URL = pu
	}
	return nil
}

// fetchSiteInfo 抓取并解析站点信息。
func fetchSiteInfo(ctx context.Context, target *url.URL) (siteData, error) {
	var out siteData
	htmlStr, finalURL, err := fetchHTML(ctx, target)
	if err != nil {
		return out, err
	}
	out.Title, out.Desc, out.Icon = parseInfo(htmlStr, finalURL)
	return out, nil
}

// realURLCtxKey 标记经代理请求时的真实目标地址：初始请求在 fetchHTML
// 记录、每次重定向在 checkRedirect 更新，供响应后还原页面最终 URL。
type realURLCtxKey struct{}

func fetchHTML(ctx context.Context, target *url.URL) (string, *url.URL, error) {
	pu, viaProxy, err := resolveProxy(ctx, target)
	if err != nil {
		return "", nil, err
	}
	if !viaProxy {
		if unsafeHost(ctx, target.Hostname()) {
			return "", nil, fmt.Errorf("%w: %s", utils.ErrUnsafeHost, target.Hostname())
		}
	} else {
		ctx = context.WithValue(ctx, realURLCtxKey{}, target.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pu.String(), nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLSize))
	if err != nil {
		return "", nil, err
	}
	// 页面最终 URL：经代理时从上下文还原真实地址（代理地址不可作为相对
	// 路径解析基准）；无代理时 resp.Request.URL 即跟随重定向后的最终 URL。
	base := resp.Request.URL
	if real, ok := resp.Request.Context().Value(realURLCtxKey{}).(string); ok {
		if u, err := url.Parse(real); err == nil {
			base = u
		}
	}
	return string(body), base, nil
}

// parseInfo 提取 title/desc/icon，优先级：
//
//	title: <title> 元素（存在即不再回退 og:title）→ og:title
//	desc:  og:description → meta[name=description]
//	icon:  apple-touch-icon → icon → og:image → twitter:image →
//	       其他含 icon token 的 link
//
// data:image 视为无图标继续回退；相对路径以页面最终 URL 解析。
func parseInfo(htmlStr string, base *url.URL) (title, desc, icon string) {
	z := html.NewTokenizer(strings.NewReader(htmlStr))

	var (
		titleText strings.Builder
		inTitle   bool
		titleSeen bool

		ogTitle    string
		ogDesc     string
		nameDesc   string
		ogImage    string
		twitterImg string

		appleHref    string // rel token 以 apple-touch-icon 开头
		iconHref     string // rel token 为 icon
		iconLikeHref string // rel token 为 icon 或以 -icon 结尾（最后回退）
	)

loop:
	for {
		switch z.Next() {
		case html.ErrorToken:
			break loop
		case html.StartTagToken:
			t := z.Token()
			if t.Data == "title" && !titleSeen {
				inTitle = true
				continue
			}
			switch t.Data {
			case "meta":
				prop := attrVal(t, "property")
				name := attrVal(t, "name")
				content := attrVal(t, "content")
				switch {
				case prop == "og:title" && ogTitle == "":
					ogTitle = content
				case prop == "og:description" && ogDesc == "":
					ogDesc = content
				case prop == "og:image" && ogImage == "":
					ogImage = content
				case prop == "twitter:image" && twitterImg == "":
					twitterImg = content
				case name == "description" && nameDesc == "":
					nameDesc = content
				}
			case "link":
				href := attrVal(t, "href")
				rel := attrVal(t, "rel")
				if href == "" || rel == "" || strings.HasPrefix(href, "data:") {
					continue
				}
				for _, tok := range strings.Fields(rel) {
					tok = strings.ToLower(tok)
					if appleHref == "" && strings.HasPrefix(tok, "apple-touch-icon") {
						appleHref = href
					}
					if iconHref == "" && tok == "icon" {
						iconHref = href
					}
					if iconLikeHref == "" && (tok == "icon" || strings.HasSuffix(tok, "-icon")) {
						iconLikeHref = href
					}
				}
			}
		case html.TextToken:
			if inTitle {
				titleText.Write(z.Text())
			}
		case html.EndTagToken:
			if z.Token().Data == "title" && inTitle {
				inTitle = false
				titleSeen = true
			}
		}
	}

	switch {
	case appleHref != "":
		icon = appleHref
	case iconHref != "":
		icon = iconHref
	case ogImage != "":
		icon = ogImage
	case twitterImg != "":
		icon = twitterImg
	}
	if strings.HasPrefix(icon, "data:image") {
		icon = ""
	}
	if icon == "" {
		icon = iconLikeHref
	}
	if icon != "" {
		icon = resolveIcon(base, icon)
	}

	title = strings.TrimSpace(titleText.String())
	if !titleSeen {
		title = ogTitle
	}
	desc = ogDesc
	if desc == "" {
		desc = nameDesc
	}
	return title, desc, icon
}

// resolveIcon 按页面最终 URL 解析相对图标路径，仅接受 http/https 结果。
func resolveIcon(base *url.URL, icon string) string {
	ref, err := url.Parse(icon)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}
	return resolved.String()
}

func attrVal(t html.Token, name string) string {
	for _, a := range t.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// fetchIcon 请求图标 URL；返回的响应体由调用方负责关闭。
func fetchIcon(ctx context.Context, iconURL string) (*http.Response, error) {
	u, err := url.Parse(iconURL)
	if err != nil {
		return nil, err
	}
	pu, viaProxy, err := resolveProxy(ctx, u)
	if err != nil {
		return nil, err
	}
	if !viaProxy && unsafeHost(ctx, u.Hostname()) {
		return nil, fmt.Errorf("%w: %s", utils.ErrUnsafeHost, u.Hostname())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pu.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	return httpClient.Do(req)
}

// fetchIconBase64 抓取图标并转为 data URL；失败返回空串。
func fetchIconBase64(ctx context.Context, iconURL string) string {
	if strings.HasPrefix(iconURL, "data:image") {
		return iconURL
	}
	resp, err := fetchIcon(ctx, iconURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIconSize+1))
	if err != nil || len(body) > maxIconSize {
		return ""
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/x-icon"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body)
}
