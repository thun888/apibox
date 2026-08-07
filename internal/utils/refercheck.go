package utils

import (
	"net/url"
	"strings"
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

// IsAllowed 检查 host 是否命中白名单（后缀匹配）
func IsAllowed(allowedReferers []string, host string) bool {
	for _, allowed := range allowedReferers {
		if strings.HasSuffix(host, allowed) {
			return true
		}
	}
	return false
}
