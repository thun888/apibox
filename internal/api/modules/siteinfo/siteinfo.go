// Package siteinfo 网页信息抓取（site-info-api 移植）：
//
//	GET /api/siteinfo/info?url=...&base64=1   站点信息 JSON
//	GET /api/siteinfo/icon?url=...            站点图标二进制代理
//
// 相比原版：仅保留 site 类型（type 参数兼容）、修复相对 icon 解析与
// rel 匹配、抓取带超时与体积上限、Redis 缓存 30 天（站点元数据，缓存
// key 按规范化 URL 构建）、icon 接口不落 Redis 直接转发（<2MB，超限
// 302 跳转原图地址）、SSRF 防护
// （拦截解析到内网/环回/链路本地等保留地址的目标，含重定向目标）。
package siteinfo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/idna"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/cache"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/utils"
)

const (
	moduleName = "siteinfo"

	cacheTTL = 30 * 24 * time.Hour // Redis 缓存 30 天

	// cdnCacheTTL 与原版 Vercel-CDN-Cache-Control 一致，供浏览器/CDN 缓存
	cdnCacheTTL = 604800
)

var log = utils.NewModuleLogger(moduleName)

// siteData 返回结构与原版一致；字段全空时序列化为 {}。
type siteData struct {
	Title      string `json:"title,omitempty"`
	Desc       string `json:"desc,omitempty"`
	Icon       string `json:"icon,omitempty"`
	IconBase64 string `json:"iconBase64,omitempty"`
	URL        string `json:"url,omitempty"`
}

func (d siteData) empty() bool {
	return d.Title == "" && d.Desc == "" && d.Icon == ""
}

// Controller 站点信息模块
type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

func (c *Controller) ModuleName() string { return moduleName }

func (c *Controller) Enabled() bool {
	return config.Cfg.Modules.SiteInfo.Enabled()
}

func (c *Controller) Register(r *gin.RouterGroup) {
	r.GET("/info", handle)
	r.GET("/icon", handlePureIcon)
}

// siteDataCacheKey 站点信息缓存 key：基于规范化后的 URL 构建，语义等价的
// 请求（大小写、默认端口、fragment、参数顺序等差异）共享缓存条目；
// base64 变体使用独立命名空间（icon 接口复用普通变体的 key），
// 避免旧格式 "siteinfo:<url>|%t" 中 URL 含 "|" 后缀时可能的 key 碰撞。
func siteDataCacheKey(normalized string, needBase64 bool) string {
	if needBase64 {
		return "siteinfo:data64:" + normalized
	}
	return "siteinfo:data:" + normalized
}

// normalizeTarget 规范化 URL 用作缓存 key：统一 scheme/host 大小写与 IDN、
// 剥离用户信息与默认端口、丢弃 fragment、空路径补 "/"、清理点分路径段并
// 按字典序重排查询参数，使等价请求共享缓存条目。
func normalizeTarget(u *url.URL) string {
	clone := *u
	clone.User = nil

	clone.Scheme = strings.ToLower(clone.Scheme)

	host := strings.ToLower(clone.Hostname())
	if ascii, err := idna.Lookup.ToASCII(host); err == nil {
		host = ascii
	}
	port := clone.Port()
	if port != "" && ((clone.Scheme == "http" && port == "80") ||
		(clone.Scheme == "https" && port == "443")) {
		port = ""
	}
	switch {
	case port != "":
		clone.Host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		clone.Host = "[" + host + "]"
	default:
		clone.Host = host
	}

	clone.Fragment = ""
	if clone.Path == "" {
		clone.Path = "/"
	} else {
		clone.Path = path.Clean(clone.Path)
	}
	clone.RawPath = "" // 丢弃解析时的转义细节，统一使用规范化后的 Path
	clone.ForceQuery = false
	clone.RawQuery = clone.Query().Encode()
	return clone.String()
}

func checkAllowed(ctx *gin.Context) bool {
	if utils.CheckReferer(config.Cfg.Modules.SiteInfo.AllowedReferers, ctx) {
		return true
	}
	ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	return false
}

// rewriteCachedURL 将缓存 JSON 中的 url 字段替换为本次请求的原始 target，
// 保证规范化 key 共享缓存条目时响应仍回显请求方传入的 URL。
func rewriteCachedURL(val, target string) ([]byte, bool) {
	var d siteData
	if err := json.Unmarshal([]byte(val), &d); err != nil {
		return nil, false
	}
	d.URL = target
	b, err := json.Marshal(d)
	return b, err == nil
}

// parseTarget 校验 url 参数：scheme 必须 http/https 且 host 非空。
func parseTarget(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, false
	}
	return u, true
}

// handle GET /api/siteinfo/info?url=...&type=site&base64=1
// type 只接受 site（其余值返回 {}）；base64=1|true 时抓取图标转 data
// URL 填入 iconBase64；未提取到任何信息、url 非法或上游失败均返回 {}。
func handle(ctx *gin.Context) {
	if !checkAllowed(ctx) {
		return
	}

	// type 只接受 site，其余值返回 {}
	if t := ctx.Query("type"); t != "" && t != "site" {
		ctx.JSON(http.StatusOK, gin.H{})
		return
	}

	target := ctx.Query("url")
	u, ok := parseTarget(target)
	if !ok {
		ctx.JSON(http.StatusOK, gin.H{})
		return
	}
	needBase64 := isTruthy(ctx.Query("base64"))

	cacheKey := siteDataCacheKey(normalizeTarget(u), needBase64)

	// ---------- JSON 结果缓存 ----------
	if cache.Client != nil {
		if val, err := cache.Client.Get(ctx, cacheKey).Result(); err == nil && val != "" {
			setCDNCache(ctx)
			// 条目按规范化 key 共享，命中时把 url 回写为本次请求的原始值
			if b, ok := rewriteCachedURL(val, target); ok {
				ctx.Data(http.StatusOK, "application/json;charset=utf-8", b)
				return
			}
			ctx.Data(http.StatusOK, "application/json;charset=utf-8", []byte(val))
			return
		}
	}

	data, err := fetchSiteInfo(ctx.Request.Context(), u)
	if err != nil {
		if errors.Is(err, utils.ErrUnsafeHost) {
			log.Warn("blocked unsafe host", "url", target)
		} else {
			log.Error("fetch site info failed", "url", target, "error", err)
		}
		ctx.JSON(http.StatusOK, gin.H{})
		return
	}
	if data.empty() {
		ctx.JSON(http.StatusOK, gin.H{})
		return
	}
	data.URL = target

	if needBase64 && data.Icon != "" {
		data.IconBase64 = fetchIconBase64(ctx.Request.Context(), data.Icon)
	}

	if cache.Client != nil {
		if b, err := json.Marshal(data); err == nil {
			if err := cache.Client.Set(ctx, cacheKey, string(b), cacheTTL).Err(); err != nil {
				log.Warn("set cache failed", "error", err)
			}
		}
	}

	setCDNCache(ctx)
	ctx.JSON(http.StatusOK, data)
}

// handlePureIcon GET /api/siteinfo/icon?url=...：代理返回站点图标二进制。
func handlePureIcon(ctx *gin.Context) {
	if !checkAllowed(ctx) {
		return
	}

	target := ctx.Query("url")
	u, ok := parseTarget(target)
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid url"})
		return
	}

	// ---------- 站点信息缓存（与主接口共享，无 base64 变体） ----------
	dataCacheKey := siteDataCacheKey(normalizeTarget(u), false)
	var (
		data siteData
		err  error
	)
	if cache.Client != nil {
		if val, err := cache.Client.Get(ctx, dataCacheKey).Result(); err == nil && val != "" {
			if err := json.Unmarshal([]byte(val), &data); err != nil {
				data = siteData{}
			}
		}
	}
	if data.URL == "" { // 未命中缓存（缓存条目必然带 url）
		data, err = fetchSiteInfo(ctx.Request.Context(), u)
		if err != nil {
			if errors.Is(err, utils.ErrUnsafeHost) {
				ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
				return
			}
			log.Error("fetch site info failed", "url", target, "error", err)
			ctx.JSON(http.StatusBadGateway, gin.H{"error": "upstream error"})
			return
		}
		if data.empty() {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "Icon not found"})
			return
		}
		data.URL = target
		if cache.Client != nil {
			if b, err := json.Marshal(data); err == nil {
				if err := cache.Client.Set(ctx, dataCacheKey, string(b), cacheTTL).Err(); err != nil {
					log.Warn("set cache failed", "error", err)
				}
			}
		}
	}
	if data.Icon == "" {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "Icon not found"})
		return
	}

	serveIcon(ctx, data.Icon)
}

func setCDNCache(ctx *gin.Context) {
	ctx.Header("Cache-Control", fmt.Sprintf("public, max-age=%d", cdnCacheTTL))
}

// serveIcon 直接转发图标二进制：体积小于 maxIconSize（2MB）时转发；
// 达到或超过上限返回 302 跳转到原图标地址，由客户端自行获取。
// 不落 Redis；响应带 Cache-Control 交给 CDN/浏览器缓存。
func serveIcon(ctx *gin.Context, iconURL string) {
	resp, err := fetchIcon(ctx.Request.Context(), iconURL)
	if err != nil {
		if errors.Is(err, utils.ErrUnsafeHost) {
			ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		log.Error("fetch icon failed", "icon", iconURL, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch icon"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "Icon not found"})
		return
	}

	// 上游已声明体积超限时直接 302，避免白拉大对象
	if resp.ContentLength >= maxIconSize {
		setCDNCache(ctx)
		ctx.Redirect(http.StatusFound, iconURL)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/x-icon"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIconSize))
	if err != nil {
		log.Error("read icon failed", "icon", iconURL, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch icon"})
		return
	}
	if len(body) >= maxIconSize { // 读到上限说明体积 >= 2MB，302 跳转原地址
		setCDNCache(ctx)
		ctx.Redirect(http.StatusFound, iconURL)
		return
	}

	ctx.Header("Content-Type", contentType)
	ctx.Header("Content-Length", strconv.Itoa(len(body)))
	setCDNCache(ctx)
	_, _ = ctx.Writer.Write(body)
}

func isTruthy(v string) bool {
	return v == "1" || v == "true"
}
