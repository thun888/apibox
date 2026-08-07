package api

import "github.com/gin-gonic/gin"

// Controller 接口：所有独立的 API 模块都需实现此接口
type Controller interface {
	Register(r *gin.Engine)
}

var controllers []Controller

// RegisterController 收集所有模块的注册函数
func RegisterController(c Controller) {
	controllers = append(controllers, c)
}

// SetupRouter 统一加载所有已注册的 Controller
// mode: debug | release | test，在创建 Engine 前设置
// trustedProxies: 反向代理可信 IP 列表，影响 c.ClientIP()
func SetupRouter(mode string, trustedProxies []string) *gin.Engine {
	gin.SetMode(mode)

	r := gin.Default()

	if len(trustedProxies) > 0 {
		_ = r.SetTrustedProxies(trustedProxies)
	}

	for _, c := range controllers {
		c.Register(r)
	}
	return r
}
