package api

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Version 为构建时通过 -ldflags 注入的版本号，如 "v1.2.3"
// 未注入时默认为 "dev"
var Version = "dev"

// Controller 接口：所有独立的 API 模块都需实现此接口
type Controller interface {
	// Register 注册路由到给定的 Group（已带 /api/<moduleName> 前缀）
	Register(r *gin.RouterGroup)
	// ModuleName 返回模块名，用作路由前缀
	ModuleName() string
}

var controllers []Controller

// RegisterController 收集所有模块的注册函数
func RegisterController(c Controller) {
	controllers = append(controllers, c)
}

// SetupRouter 统一加载所有已注册的 Controller
// mode: debug | release | test，在创建 Engine 前设置
// trustedProxies: 反向代理可信 IP 列表，影响 c.ClientIP()
// allowedOrigins: CORS 允许的来源域名列表
func SetupRouter(mode string, trustedProxies []string, allowedOrigins []string) *gin.Engine {
	gin.SetMode(mode)

	r := gin.Default()

	// CORS 中间件
	if len(allowedOrigins) > 0 {
		corsConfig := cors.Config{
			AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
			ExposeHeaders:    []string{"Content-Length"},
			AllowCredentials: true,
			MaxAge:           12 * time.Hour,
		}

		// 检查是否包含 "*" 通配符
		allowAll := false
		for _, o := range allowedOrigins {
			if o == "*" {
				allowAll = true
				break
			}
		}

		if allowAll {
			// 允许任意来源：回显请求的 Origin（配合 credentials 不能用 "*"）
			corsConfig.AllowOriginFunc = func(origin string) bool { return true }
		} else {
			corsConfig.AllowOrigins = allowedOrigins
		}

		r.Use(cors.New(corsConfig))
	}

	if len(trustedProxies) > 0 {
		_ = r.SetTrustedProxies(trustedProxies)
	}

	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "https://github.com/thun888/apibox/")
	})
	// ping
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
			"version": Version,
		})
	})
	api := r.Group("/api")
	for _, c := range controllers {
		group := api.Group("/" + c.ModuleName())
		c.Register(group)
	}
	return r
}
