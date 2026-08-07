package cache

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/thun888/apibox/internal/config"

	"github.com/redis/go-redis/v9"
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, nil)).With("module", "cache")

// Client 全局 Redis 实例，模块通过 cache.Client 直接使用
var Client *redis.Client

// Init 初始化 Redis 连接
func Init(cfg config.RedisConfig) error {
	// 未配置 addr 则跳过 Redis 初始化
	if cfg.Addr == "" {
		logger.Info("Redis not configured, skipping")
		return nil
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	// 启动时先 ping 一次，确保连通
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}

	Client = rdb
	logger.Info("Redis connected", "addr", cfg.Addr, "db", cfg.DB)
	return nil
}

// Close 关闭 Redis 连接
func Close() error {
	if Client == nil {
		return nil
	}
	return Client.Close()
}
