package hitcount

import (
	"time"

	"github.com/thun888/apibox/internal/database"
)

const tablePrefix = "hitcount_" // 模块表名前缀

// Hit 访问计数表模型：键值对存储，键为路径（path），值为累计次数（count）
type Hit struct {
	Path      string    `gorm:"column:path;primaryKey;size:255" json:"path"`
	Count     int64     `gorm:"column:count;not null;default:0" json:"count"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

func (Hit) TableName() string { return database.BuildTableName(&Hit{}, tablePrefix) }

func init() {
	database.RegisterModel(&Hit{})
}
