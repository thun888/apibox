package qqmailhead

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/utils"

	"github.com/gin-gonic/gin"
)

const (
	qqMailIconAPI = "https://wx.mail.qq.com/info/geticon"
	moduleName    = "qqmail_head"
)

var emailRegex = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

var logger = utils.NewModuleLogger(moduleName)

type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

func (c *Controller) Register(r *gin.RouterGroup) {
	r.GET("/:email", c.getQQMailHead)
}

func (c *Controller) ModuleName() string { return moduleName }

func (c *Controller) getQQMailHead(ctx *gin.Context) {
	headEmail := ctx.Param("email")

	// 验证邮箱格式
	if !emailRegex.MatchString(headEmail) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email format"})
		return
	}

	logger.Info("Fetching avatar", "email", headEmail)

	iconURL := fmt.Sprintf("%s?addr=%s&type=0", qqMailIconAPI, headEmail)

	// 获取 CookieCloud 中的 mail.qq.com 域 Cookie
	cookies, err := utils.GetCookies(ctx.Request.Context())
	if err != nil {
		logger.Warn("Failed to get cookies", "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get cookies"})
		return
	}

	mailCookies := utils.FilterCookiesByDomain(cookies, ".mail.qq.com")
	cookieHeader := utils.BuildCookieHeader(mailCookies)

	// 请求 QQ 邮箱头像 API
	req, err := http.NewRequestWithContext(ctx.Request.Context(), "GET", iconURL, nil)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Upstream error", "error", err)
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "Upstream error"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Warn("Upstream returned non-200", "status", resp.StatusCode)
		ctx.JSON(http.StatusBadGateway, gin.H{
			"error": fmt.Sprintf("Failed to fetch image, code: %d", resp.StatusCode),
		})
		return
	}

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read response body", "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/png"
	}

	// 设置缓存头
	ctx.Header("Cache-Control", "public, max-age=2592000") // 30 天
	ctx.Data(http.StatusOK, contentType, body)
}
