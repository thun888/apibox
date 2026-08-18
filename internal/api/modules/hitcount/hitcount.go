// Package hitcount 提供访问计数（hits）模块，参考 dwyl/hits 的用法，
// 但不沿用其数据库设计：计数按「键值对」存储，键为路径、值为累计次数
// （表 hitcount_hits，主键 path）。
//
// 两个访问端点共享同一个计数器（键为去掉 .svg/.json 后缀的路径）：
//
//	GET /api/hit_count/<path>.svg   → SVG 徽章（image/svg+xml）
//	GET /api/hit_count/<path>.json  → JSON（shields.io endpoint 格式）
//
// 计数流程：请求先写入内存缓冲并立即返回「数据库累计值 + 缓冲增量」，
// 后台协程每 5 分钟（可通过 sync_interval 配置）把缓冲增量 upsert 进
// 数据库后释放缓冲；进程优雅退出时也会 flush 一次，避免丢失最近一个
// 周期的计数。
package hitcount

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/utils"
)

const moduleName = "hit_count"

var log = utils.NewModuleLogger(moduleName)

// Controller hitcount 模块控制器
type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

// Register 注册路由
func (c *Controller) Register(r *gin.RouterGroup) {
	r.GET("/*path", handleHit)
}

func (c *Controller) ModuleName() string { return moduleName }

func (c *Controller) Enabled() bool { return config.Cfg.Modules.HitCount.Enabled() }

// Shutdown 进程退出前调用：停止同步循环并把缓冲计数写入数据库
func (c *Controller) Shutdown() { stopAndFlush() }

// handleHit GET /api/hit_count/<path>.svg | GET /api/hit_count/<path>.json
func handleHit(ctx *gin.Context) {
	key, format, ok := extractKey(ctx.Param("path"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid path, expected /api/hit_count/<path>.svg or /api/hit_count/<path>.json",
		})
		return
	}

	count := Incr(key)

	// 计数每次请求都会变化，禁止浏览器/CDN 任何一层缓存
	ctx.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	ctx.Header("Pragma", "no-cache")
	ctx.Header("Expires", "0")

	if format == "svg" {
		renderSVG(ctx, count)
		return
	}
	renderJSON(ctx, count)
}

// renderJSON 输出 shields.io endpoint 格式的 JSON 徽章数据
func renderJSON(ctx *gin.Context, count int64) {
	ctx.JSON(http.StatusOK, gin.H{
		"schemaVersion": 1,
		"label":         normLabel(ctx.Query("label")),
		"message":       strconv.FormatInt(count, 10),
		"color":         normColor(ctx.Query("color")),
		"style":         normStyle(ctx.Query("style")),
	})
}

// extractKey 校验并解析路径参数（gin 通配参数带前导 "/"），
// 返回去后缀的计数键与响应格式。键允许字母、数字、"-"、"_"、"." 与
// 段间分隔符 "/"，长度 ≤ 255。
func extractKey(raw string) (key, format string, ok bool) {
	raw = strings.TrimPrefix(raw, "/")
	switch {
	case strings.HasSuffix(raw, ".svg"):
		format = "svg"
	case strings.HasSuffix(raw, ".json"):
		format = "json"
	default:
		return "", "", false
	}
	key = strings.TrimSuffix(raw, "."+format)
	if key == "" || len(key) > 255 {
		return "", "", false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" {
			return "", "", false
		}
		for _, ch := range seg {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' ||
				ch == '-' || ch == '_' || ch == '.') {
				return "", "", false
			}
		}
	}
	return key, format, true
}
