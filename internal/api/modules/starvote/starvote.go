package starvote

import (
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/database"
	"github.com/thun888/apibox/internal/utils"
)

var (
	moduleName = "starvote"
)
var logger = utils.NewModuleLogger(moduleName)

// Controller StarVote 投票/评分模块控制器
type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

// Register 注册路由
func (c *Controller) Register(r *gin.RouterGroup) {
	r.POST("/rating/update", c.updateRating)
	r.POST("/vote/update", c.updateVote)
	r.GET("/vote/info", c.getVoteInfo)
	r.GET("/rating/info", c.getRatingInfo)
}

func (c *Controller) ModuleName() string { return moduleName }

// checkReferer 校验 Referer 是否在白名单中
func checkReferer(ctx *gin.Context) bool {
	referer := ctx.GetHeader("Referer")
	if referer == "" {
		return false
	}
	refererHost, err := utils.ExtractHost(referer)
	if err != nil {
		return false
	}
	return utils.IsAllowed(config.Cfg.Modules.StarVote.AllowedReferers, refererHost)
}

// getParam 优先从 POST form 获取参数，fallback 到 Query
func getParam(ctx *gin.Context, key string) string {
	v := ctx.PostForm(key)
	if v != "" {
		return v
	}
	return ctx.Query(key)
}

// ---------------------------------------------------------------------------
// 数据库操作
// ---------------------------------------------------------------------------

// upsertVote 投票 Upsert — id 不存在则插入，存在则对应字段 +1
func upsertVote(id, field string) error {
	vote := Vote{ID: id}
	switch field {
	case "up":
		vote.Up = 1
	case "down":
		vote.Down = 1
	}

	return database.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			field:        gorm.Expr("? + 1", clause.Column{Name: field}),
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}),
	}).Create(&vote).Error
}

// upsertRating 评分 Upsert — id 不存在则插入，存在则对应评分列 +1
func upsertRating(id string, value int) error {
	colName := strconv.Itoa(value)
	rating := Rating{ID: id}
	switch value {
	case 1:
		rating.R1 = 1
	case 2:
		rating.R2 = 1
	case 3:
		rating.R3 = 1
	case 4:
		rating.R4 = 1
	case 5:
		rating.R5 = 1
	}

	return database.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			colName:      gorm.Expr("? + 1", clause.Column{Name: colName}),
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}),
	}).Create(&rating).Error
}

// ---------------------------------------------------------------------------
// POST /api/rating/update  —  评分更新 (value: 1-5，向上取整)
// ---------------------------------------------------------------------------
func (c *Controller) updateRating(ctx *gin.Context) {
	if !checkReferer(ctx) {
		ctx.Status(http.StatusForbidden)
		return
	}

	id := getParam(ctx, "id")
	rawValue := getParam(ctx, "value")

	if id == "" || rawValue == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "Bad Request"})
		return
	}

	floatVal, err := strconv.ParseFloat(rawValue, 64)
	if err != nil || floatVal < 1 || floatVal > 5 {
		ctx.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "Bad Request"})
		return
	}

	v := int(math.Ceil(floatVal))
	if err := upsertRating(id, v); err != nil {
		logger.Error("Database error during rating update", "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"code": 400, "message": "Database error during rating update."})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"success": "true"})
}

// ---------------------------------------------------------------------------
// POST /api/vote/update     —  投票更新 (value: up | down)
// ---------------------------------------------------------------------------
func (c *Controller) updateVote(ctx *gin.Context) {
	if !checkReferer(ctx) {
		ctx.Status(http.StatusForbidden)
		return
	}

	id := getParam(ctx, "id")
	value := getParam(ctx, "value")

	if id == "" || (value != "up" && value != "down") {
		ctx.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "Bad Request"})
		return
	}

	if err := upsertVote(id, value); err != nil {
		logger.Error("Database error during vote update", "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "Database error during vote update."})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"success": "true"})
}

// ---------------------------------------------------------------------------
// GET  /api/vote/info       —  查询投票信息
// ---------------------------------------------------------------------------
func (c *Controller) getVoteInfo(ctx *gin.Context) {
	if !checkReferer(ctx) {
		ctx.Status(http.StatusForbidden)
		return
	}

	id := ctx.Query("id")
	if id == "" {
		id = "default"
	}

	var vote Vote
	if err := database.DB.Where("id = ?", id).First(&vote).Error; err != nil {
		ctx.JSON(http.StatusOK, gin.H{
			"votes": gin.H{
				"id":        id,
				"up":        0,
				"down":      0,
				"createdAt": nil,
				"updatedAt": nil,
			},
		})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"votes": gin.H{
			"id":        vote.ID,
			"up":        vote.Up,
			"down":      vote.Down,
			"createdAt": vote.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"updatedAt": vote.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		},
	})
}

// ---------------------------------------------------------------------------
// GET  /api/rating/info     —  查询评分信息
// ---------------------------------------------------------------------------
func (c *Controller) getRatingInfo(ctx *gin.Context) {
	if !checkReferer(ctx) {
		ctx.Status(http.StatusForbidden)
		return
	}

	id := ctx.Query("id")
	if id == "" {
		id = "default"
	}

	var rating Rating
	if err := database.DB.Where("id = ?", id).First(&rating).Error; err != nil {
		ctx.JSON(http.StatusOK, gin.H{
			"rating": gin.H{
				"id":        id,
				"1":         0,
				"2":         0,
				"3":         0,
				"4":         0,
				"5":         0,
				"createdAt": nil,
				"updatedAt": nil,
			},
		})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"rating": gin.H{
			"id":        rating.ID,
			"1":         rating.R1,
			"2":         rating.R2,
			"3":         rating.R3,
			"4":         rating.R4,
			"5":         rating.R5,
			"createdAt": rating.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"updatedAt": rating.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		},
	})
}
