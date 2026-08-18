package hitcount

import (
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/database"
)

// defaultSyncInterval 内存缓冲同步到数据库的默认间隔
const defaultSyncInterval = 5 * time.Minute

// entry 单条路径的内存计数：base 为最近一次从数据库读到的累计值，
// delta 为本次缓冲周期内尚未写库的增量。
type entry struct {
	base  int64
	delta int64
}

var (
	bufferMu sync.Mutex
	buffer   = make(map[string]*entry)

	stopCh   = make(chan struct{})
	stopOnce sync.Once
	loopOnce sync.Once
)

// syncInterval 返回配置的缓冲同步间隔；未配置或非法时回退默认 5 分钟
func syncInterval() time.Duration {
	if config.Cfg != nil {
		if d := config.Cfg.Modules.HitCount.SyncInterval; d > 0 {
			return d
		}
	}
	return defaultSyncInterval
}

// startSyncLoop 惰性启动后台同步循环（首次计数时启动，只启动一次）
func startSyncLoop() {
	loopOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(syncInterval())
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					Flush()
				case <-stopCh:
					return
				}
			}
		}()
	})
}

// Incr 记录一次访问并返回该路径的最新累计次数。
// 计数先写入内存缓冲；首次访问某路径时需要一次数据库读取作为基准，
// 之后的请求全部走内存，由后台循环定期同步到数据库。
func Incr(path string) int64 {
	startSyncLoop()

	bufferMu.Lock()
	defer bufferMu.Unlock()

	e, ok := buffer[path]
	if !ok {
		base, err := loadBase(path)
		if err != nil {
			log.Warn("load base count failed, counting from zero", "path", path, "error", err)
		}
		e = &entry{base: base}
		buffer[path] = e
	}
	e.delta++
	return e.base + e.delta
}

// Flush 把缓冲中的增量写入数据库并释放缓存。
// 先整体换出缓冲再逐条写库，写库期间的新请求进入新缓冲、下个周期再同步；
// 写库失败的路径会合并回缓冲，下个周期重试，避免丢计数。
func Flush() {
	bufferMu.Lock()
	if len(buffer) == 0 {
		bufferMu.Unlock()
		return
	}
	snapshot := buffer
	buffer = make(map[string]*entry)
	bufferMu.Unlock()

	failed := 0
	for path, e := range snapshot {
		if e.delta == 0 {
			continue
		}
		if err := upsertDelta(path, e.delta); err != nil {
			failed++
			log.Error("flush hit count failed", "path", path, "delta", e.delta, "error", err)
			// 写库失败：合并回当前缓冲，下个周期重试
			bufferMu.Lock()
			if cur, ok := buffer[path]; ok {
				cur.delta += e.delta
			} else {
				buffer[path] = &entry{base: e.base, delta: e.delta}
			}
			bufferMu.Unlock()
		}
	}

	if synced := len(snapshot) - failed; synced > 0 {
		log.Info("hit count synced to database", "paths", synced, "failed", failed)
	}
}

// stopAndFlush 停止同步循环并立即 flush（幂等，供进程退出时调用）
func stopAndFlush() {
	stopOnce.Do(func() { close(stopCh) })
	Flush()
}

// loadBase 读取路径在数据库中的累计计数；不存在返回 0
func loadBase(path string) (int64, error) {
	var hit Hit
	err := database.DB.Where("path = ?", path).First(&hit).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return hit.Count, nil
}

// upsertDelta 把增量累加进数据库：行不存在则插入，存在则 count += delta
func upsertDelta(path string, delta int64) error {
	hit := Hit{Path: path, Count: delta}
	return database.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "path"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"count":      gorm.Expr("? + ?", clause.Column{Name: "count"}, delta),
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}),
	}).Create(&hit).Error
}
