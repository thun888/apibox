package api

import "github.com/gin-gonic/gin"

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
func SetupRouter(mode string, trustedProxies []string) *gin.Engine {
	gin.SetMode(mode)

	r := gin.Default()

	if len(trustedProxies) > 0 {
		_ = r.SetTrustedProxies(trustedProxies)
	}

	api := r.Group("/api")
	for _, c := range controllers {
		group := api.Group("/" + c.ModuleName())
		c.Register(group)
	}
	return r
}
