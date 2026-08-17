package utils

import (
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// ExtractHost 从完整 URL 中提取 host
func ExtractHost(rawURL string) (string, error) {
	if !strings.Contains(rawURL, "://") {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}

// IsAllowed 检查 host 是否命中白名单。
// 白名单项为 "*" 时允许任意 host；其余项按后缀匹配
// （如 "example.com" 命中 "sub.example.com"）。
func IsAllowed(allowedReferers []string, host string) bool {
	for _, allowed := range allowedReferers {
		if allowed == "*" {
			return true
		}
		if strings.HasSuffix(host, allowed) {
			return true
		}
	}
	return false
}

// CheckReferer 提取请求中的 Referer 头，校验其 host 是否在白名单中。
// 若 Referer 为空、解析失败或不在白名单，返回 false。
func CheckReferer(allowedReferers []string, ctx *gin.Context) bool {
	referer := ctx.GetHeader("Referer")
	if referer == "" {
		return false
	}
	refererHost, err := ExtractHost(referer)
	if err != nil {
		return false
	}
	return IsAllowed(allowedReferers, refererHost)
}
