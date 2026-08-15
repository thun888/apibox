// Package siteinfo 网页信息抓取（site-info-api 移植）：
//
//	GET /api/siteinfo/info?url=...&base64=1   站点信息 JSON
//	GET /api/siteinfo/icon?url=...            站点图标二进制代理
//
// 相比原版：仅保留 site 类型（type 参数兼容）、修复相对 icon 解析与
// rel 匹配、抓取带超时与体积上限、Redis 缓存 30 天、SSRF 防护
// （拦截解析到内网/环回/链路本地等保留地址的目标，含重定向目标）。
package siteinfo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

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

// siteDataCacheKey 站点信息缓存 key；base64 变体分开缓存，
// icon 接口复用无 base64 变体的 key。
func siteDataCacheKey(target string, needBase64 bool) string {
	return fmt.Sprintf("siteinfo:%s|%t", target, needBase64)
}

func checkAllowed(ctx *gin.Context) bool {
	if utils.CheckReferer(config.Cfg.Modules.SiteInfo.AllowedReferers, ctx) {
		return true
	}
	ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	return false
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

	cacheKey := siteDataCacheKey(target, needBase64)

	// ---------- JSON 结果缓存 ----------
	if cache.Client != nil {
		if val, err := cache.Client.Get(ctx, cacheKey).Result(); err == nil && val != "" {
			setCDNCache(ctx)
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
	dataCacheKey := siteDataCacheKey(target, false)
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

// serveIcon 代理返回图标二进制；图标本体缓存于 Redis，超过 4MB 不缓存直接转发。
func serveIcon(ctx *gin.Context, iconURL string) {
	bodyKey := "siteinfo:icon:" + iconURL
	mimeKey := "siteinfo:icon:mime:" + iconURL

	// ---------- 图标缓存 ----------
	if cache.Client != nil {
		if body, err := cache.Client.Get(ctx, bodyKey).Bytes(); err == nil && len(body) > 0 {
			mime, _ := cache.Client.Get(ctx, mimeKey).Result()
			if mime == "" {
				mime = "image/x-icon"
			}
			ctx.Header("Content-Type", mime)
			ctx.Header("Content-Length", strconv.Itoa(len(body)))
			setCDNCache(ctx)
			_, _ = ctx.Writer.Write(body)
			return
		}
	}

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

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/x-icon"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIconSize+1))
	if err != nil {
		log.Error("read icon failed", "icon", iconURL, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch icon"})
		return
	}

	ctx.Header("Content-Type", contentType)
	setCDNCache(ctx)
	if len(body) > maxIconSize {
		// 超限不缓存：先输出已读部分，再转发剩余
		if resp.ContentLength >= 0 {
			ctx.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
		}
		_, _ = ctx.Writer.Write(body)
		_, _ = io.Copy(ctx.Writer, resp.Body)
		return
	}

	if cache.Client != nil {
		if err := cache.Client.Set(ctx, bodyKey, string(body), cacheTTL).Err(); err != nil {
			log.Warn("set icon cache failed", "icon", iconURL, "error", err)
		} else if err := cache.Client.Set(ctx, mimeKey, contentType, cacheTTL).Err(); err != nil {
			log.Warn("set icon mime cache failed", "icon", iconURL, "error", err)
		}
	}
	ctx.Header("Content-Length", strconv.Itoa(len(body)))
	_, _ = ctx.Writer.Write(body)
}

func isTruthy(v string) bool {
	return v == "1" || v == "true"
}
