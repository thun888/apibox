package biliinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/cache"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/utils"

	"github.com/gin-gonic/gin"
)

const (
	bilibiliAPI = "https://api.bilibili.com/x/web-interface/view"
	cacheTTL    = 1 * time.Hour // 1 小时缓存
	moduleName  = "bili_info"
)

var logger = utils.NewModuleLogger(moduleName)

type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

func (c *Controller) Register(r *gin.RouterGroup) {
	r.GET("/get_video_info", c.getVideoInfo)
}

func (c *Controller) ModuleName() string { return moduleName }

func (c *Controller) Enabled() bool { return config.Cfg.Modules.BiliInfo.Enabled() }

func (c *Controller) getVideoInfo(ctx *gin.Context) {
	// Referer 校验
	if !utils.CheckReferer(config.Cfg.Modules.BiliInfo.AllowedReferers, ctx) {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// 获取 bvid 参数
	bvid := ctx.Query("bvid")
	if bvid == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "missing bvid"})
		return
	}

	cacheKey := fmt.Sprintf("video:bvid:%s", bvid)

	// 检查缓存
	if cache.Client != nil {
		cached, err := cache.Client.Get(context.Background(), cacheKey).Result()
		if err == nil && cached != "" {
			logger.Info("Cache hit", "bvid", bvid)
			ctx.Data(http.StatusOK, "application/json", []byte(cached))
			return
		}
	}

	// 请求 Bilibili API
	logger.Info("Fetching from Bilibili API", "bvid", bvid)
	resp, err := fetchBilibili(ctx.Request.Context(), bvid)
	if err != nil {
		logger.Error("Bilibili API error", "error", err)
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "upstream error"})
		return
	}

	body, _ := json.Marshal(resp)

	// 写入缓存
	if cache.Client != nil {
		cache.Client.Set(context.Background(), cacheKey, string(body), cacheTTL)
	}

	ctx.Data(http.StatusOK, "application/json", body)
}

// fetchBilibili 带 UA/Referer/Cookie 请求 Bilibili API
func fetchBilibili(ctx context.Context, bvid string) (interface{}, error) {
	apiURL := fmt.Sprintf("%s?bvid=%s", bilibiliAPI, bvid)

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.Header.Set("Origin", "https://www.bilibili.com/")

	// 获取 bilibili 域的 Cookie
	cookies, err := utils.GetCookies(ctx)
	if err != nil {
		logger.Warn("Failed to get cookies", "error", err)
	} else {
		biliCookies := utils.FilterCookiesByDomain(cookies, ".bilibili.com")
		if len(biliCookies) > 0 {
			req.Header.Set("Cookie", utils.BuildCookieHeader(biliCookies))
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse bilibili response: %w", err)
	}

	return result, nil
}
