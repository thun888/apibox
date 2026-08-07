package order

import (
	"net/http"

	"github.com/thun888/apibox/internal/api"

	"github.com/gin-gonic/gin"
)

type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

func (c *Controller) Register(r *gin.Engine) {
	group := r.Group("/order")
	{
		group.GET("/", c.getOrder)
	}
}

func (c *Controller) getOrder(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Order info"})
}
