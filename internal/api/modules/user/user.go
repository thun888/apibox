package user

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/cache"
	"github.com/thun888/apibox/internal/database"

	"github.com/gin-gonic/gin"
)

type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
	database.RegisterModel(&User{})
}

func (c *Controller) Register(r *gin.Engine) {
	group := r.Group("/user")
	{
		group.GET("/", c.list)
		group.GET("/:id", c.get)
		group.POST("/", c.create)
		group.PUT("/:id", c.update)
		group.DELETE("/:id", c.delete)
	}
}

// list 用户列表
func (c *Controller) list(ctx *gin.Context) {
	var users []User
	if err := database.DB.Find(&users).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": users})
}

// get 获取单个用户（Cache-Aside 模式）
func (c *Controller) get(ctx *gin.Context) {
	id := ctx.Param("id")
	cacheKey := fmt.Sprintf("user:%s", id)

	// 1. 尝试从 Redis 读取
	if cache.Client != nil {
		data, err := cache.Client.Get(context.Background(), cacheKey).Result()
		if err == nil {
			var user User
			if json.Unmarshal([]byte(data), &user) == nil {
				ctx.JSON(http.StatusOK, gin.H{"data": user, "cache": true})
				return
			}
		}
	}

	// 2. 回源数据库
	idVal, err := strconv.Atoi(id)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var user User
	if err := database.DB.First(&user, idVal).Error; err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// 3. 写入缓存（10 分钟过期）
	if cache.Client != nil {
		jsonBytes, _ := json.Marshal(user)
		cache.Client.Set(context.Background(), cacheKey, jsonBytes, 10*time.Minute)
	}

	ctx.JSON(http.StatusOK, gin.H{"data": user})
}

// create 创建用户（同时清除相关缓存）
func (c *Controller) create(ctx *gin.Context) {
	var user User
	if err := ctx.ShouldBindJSON(&user); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := database.DB.Create(&user).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{"data": user})
}

// update 更新用户（同时清除缓存）
func (c *Controller) update(ctx *gin.Context) {
	id := ctx.Param("id")
	idVal, err := strconv.Atoi(id)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var user User
	if err := database.DB.First(&user, idVal).Error; err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if err := ctx.ShouldBindJSON(&user); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := database.DB.Save(&user).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 清除缓存，下次请求时会重新加载
	if cache.Client != nil {
		cache.Client.Del(context.Background(), fmt.Sprintf("user:%s", id))
	}

	ctx.JSON(http.StatusOK, gin.H{"data": user})
}

// delete 删除用户（同时清除缓存）
func (c *Controller) delete(ctx *gin.Context) {
	id := ctx.Param("id")
	idVal, err := strconv.Atoi(id)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := database.DB.Delete(&User{}, idVal).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 清除缓存
	if cache.Client != nil {
		cache.Client.Del(context.Background(), fmt.Sprintf("user:%s", id))
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
