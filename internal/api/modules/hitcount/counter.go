package hitcount

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/thun888/apibox/internal/cache"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/database"
)

// defaultSyncInterval Redis 缓冲同步到数据库的默认间隔
const defaultSyncInterval = 5 * time.Minute

// redisDeltaKey Redis 增量缓冲：单个 HASH，field 为路径、value 为未同步增量
const redisDeltaKey = "hitcount:delta"

// flushScript 原子「取出并清空」Redis 增量缓冲：
// HGETALL 后立即 DEL，避免与并发 HINCRBY 竞争导致丢计数。
var flushScript = redis.NewScript(`
local fields = redis.call("HGETALL", KEYS[1])
if next(fields) ~= nil then
  redis.call("DEL", KEYS[1])
end
return fields
`)

var (
	bufferMu sync.Mutex
	// baseCache 本缓冲周期内已从数据库读出的累计基准值，flush 后清空，
	// 保证每个路径每个周期最多一次数据库读取。
	baseCache = make(map[string]int64)

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
// 增量写入 Redis HASH（多实例共享、重启不丢）；Redis 出错时该次点击
// 不计数，返回数据库基准值。要求 Redis 已配置（见 handleHit，未配置时
// 接口直接 503，不会走到这里）。
func Incr(path string) int64 {
	startSyncLoop()

	base := loadBaseCached(path)

	n, err := cache.Client.HIncrBy(context.Background(), redisDeltaKey, path, 1).Result()
	if err != nil {
		log.Warn("redis incr failed, hit not counted", "path", path, "error", err)
		return base
	}
	return base + n
}

// loadBaseCached 读取路径在数据库中的累计基准值，本周期内命中缓存则不再查库
func loadBaseCached(path string) int64 {
	bufferMu.Lock()
	if base, ok := baseCache[path]; ok {
		bufferMu.Unlock()
		return base
	}
	bufferMu.Unlock()

	base, err := loadBase(path)
	if err != nil {
		log.Warn("load base count failed, counting from zero", "path", path, "error", err)
	}

	bufferMu.Lock()
	if cached, ok := baseCache[path]; ok {
		// 并发请求已先写回，直接复用
		bufferMu.Unlock()
		return cached
	}
	baseCache[path] = base
	bufferMu.Unlock()
	return base
}

// Flush 把 Redis 缓冲中的增量写入数据库并释放缓冲。
// 先由 Lua 脚本原子取出全部增量，再逐条写库；写库期间的请求进入新一轮
// 缓冲、下个周期再同步；写库失败的增量退回 Redis 缓冲，下个周期重试，
// 避免丢计数。
func Flush() {
	deltas := drainDeltas()
	if len(deltas) == 0 {
		return
	}

	failed := 0
	for path, delta := range deltas {
		if err := upsertDelta(path, delta); err != nil {
			failed++
			log.Error("flush hit count failed", "path", path, "delta", delta, "error", err)
			// 写库失败：增量退回 Redis 缓冲，下个周期重试
			if _, err := cache.Client.HIncrBy(context.Background(), redisDeltaKey, path, delta).Result(); err != nil {
				log.Error("re-queue hit count delta failed, delta lost", "path", path, "delta", delta, "error", err)
			}
		}
	}
	if synced := len(deltas) - failed; synced > 0 {
		log.Info("hit count synced to database", "paths", synced, "failed", failed)
	}

	// 基准缓存随周期失效：写库后清空，下个周期重新读取
	bufferMu.Lock()
	baseCache = make(map[string]int64)
	bufferMu.Unlock()
}

// drainDeltas 用 Lua 脚本原子取出并清空 Redis 缓冲的全部增量。
// 执行失败时数据仍留在 HASH 中，下个周期重试，不丢计数。
func drainDeltas() map[string]int64 {
	deltas := make(map[string]int64)

	res, err := flushScript.Run(context.Background(), cache.Client, []string{redisDeltaKey}).Result()
	if err != nil {
		log.Error("drain redis hitcount buffer failed", "error", err)
		return deltas
	}
	fields, ok := res.([]interface{})
	if !ok {
		return deltas
	}
	for i := 0; i+1 < len(fields); i += 2 {
		path, _ := fields[i].(string)
		raw, _ := fields[i+1].(string)
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		deltas[path] += n
	}
	return deltas
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
