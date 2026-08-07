package starvote

import (
	"time"

	"github.com/thun888/apibox/internal/database"
)

const tablePrefix = "starvote_" // 模块表名前缀

// Vote 投票表模型
type Vote struct {
	ID        string    `gorm:"column:id;primaryKey;size:255" json:"id"`
	Up        int       `gorm:"column:up;not null;default:0" json:"up"`
	Down      int       `gorm:"column:down;not null;default:0" json:"down"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

func (Vote) TableName() string { return database.BuildTableName(&Vote{}, tablePrefix) }

// Rating 评分表模型 (列名 1-5 对应评分等级)
type Rating struct {
	ID        string    `gorm:"column:id;primaryKey;size:255" json:"id"`
	R1        int       `gorm:"column:1;not null;default:0" json:"1"`
	R2        int       `gorm:"column:2;not null;default:0" json:"2"`
	R3        int       `gorm:"column:3;not null;default:0" json:"3"`
	R4        int       `gorm:"column:4;not null;default:0" json:"4"`
	R5        int       `gorm:"column:5;not null;default:0" json:"5"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

func (Rating) TableName() string { return database.BuildTableName(&Rating{}, tablePrefix) }

func init() {
	database.RegisterModel(&Vote{})
	database.RegisterModel(&Rating{})
}
