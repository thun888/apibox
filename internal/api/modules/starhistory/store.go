package starhistory

// 星标数据数据库缓存：GORM 模型 + 读写辅助。
//
// 数据源是 GitHub stargazers API，全量翻页抓取成本高且受 5000 次/小时
// 配额限制。Redis 缓存易失（重启/淘汰即丢），这里额外落库兜底，TTL 与
// Redis 一致（24h）：Redis 未命中时查库，仍未命中才回源 GitHub。过期的
// 库缓存行不直接丢弃，而是作为增量抓取的复用基准（见 github.go）。
//
// 未配置数据库（database.DB == nil）时自动跳过，不影响纯 Redis 部署。

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm/clause"

	"github.com/thun888/apibox/internal/database"
)

const tablePrefix = "starhistory_"

// StarDataCache 星标数据缓存表模型
type StarDataCache struct {
	Repo        string    `gorm:"primaryKey;size:200" json:"repo"`
	StarRecords string    `gorm:"type:text" json:"star_records"` // repoStarData.StarRecords 的 JSON
	LogoB64     string    `gorm:"type:text" json:"logo_b64"`
	FetchedAt   time.Time `gorm:"index" json:"fetched_at"`
}

// TableName starhistory_star_data_caches
func (StarDataCache) TableName() string {
	return database.BuildTableName(&StarDataCache{}, tablePrefix)
}

func init() {
	database.RegisterModel(&StarDataCache{})
}

// dbLoadStarData 批量读取数据库缓存行（不过滤新鲜度）。返回解析后的数据
// 与各行的 fetched_at：调用方按缓存 TTL 判断——新鲜的行直接使用，过期的
// 行作为增量抓取的复用基准。
func dbLoadStarData(ctx context.Context, repos []string) (map[string]*repoStarData, map[string]time.Time) {
	data := make(map[string]*repoStarData, len(repos))
	fetched := make(map[string]time.Time, len(repos))
	if database.DB == nil || len(repos) == 0 {
		return data, fetched
	}
	var rows []StarDataCache
	if err := database.DB.WithContext(ctx).
		Where("repo IN ?", repos).
		Find(&rows).Error; err != nil {
		log.Warn("load star data db cache failed", "error", err)
		return data, fetched
	}
	for i := range rows {
		row := &rows[i]
		var records []starRecord
		if json.Unmarshal([]byte(row.StarRecords), &records) != nil {
			continue
		}
		data[row.Repo] = &repoStarData{Repo: row.Repo, StarRecords: records, LogoB64: row.LogoB64}
		fetched[row.Repo] = row.FetchedAt
	}
	return data, fetched
}

// dbSaveStarData 写入/更新数据库缓存（repo 唯一键 upsert）。
func dbSaveStarData(ctx context.Context, data *repoStarData) {
	if database.DB == nil || data == nil {
		return
	}
	b, err := json.Marshal(data.StarRecords)
	if err != nil {
		return
	}
	row := StarDataCache{
		Repo:        data.Repo,
		StarRecords: string(b),
		LogoB64:     data.LogoB64,
		FetchedAt:   time.Now(),
	}
	if err := database.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "repo"}},
		DoUpdates: clause.AssignmentColumns([]string{"star_records", "logo_b64", "fetched_at"}),
	}).Create(&row).Error; err != nil {
		log.Warn("save star data db cache failed", "repo", data.Repo, "error", err)
	}
}

// dbPurgeExpired 机会式清理过期缓存行（失败仅记警告）。
func dbPurgeExpired(ctx context.Context) {
	if database.DB == nil {
		return
	}
	if err := database.DB.WithContext(ctx).
		Where("fetched_at <= ?", time.Now().Add(-cacheTTL)).
		Delete(&StarDataCache{}).Error; err != nil {
		log.Warn("purge star data db cache failed", "error", err)
	}
}
