package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/cache"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/database"
	_ "github.com/thun888/apibox/internal/api/modules" // 匿名导入，触发所有 API 模块的 init() 执行
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, nil))

func main() {
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// 初始化数据库（自动迁移在 Init 内部根据配置触发）
	if err := database.Init(cfg.Database); err != nil {
		logger.Error("Failed to init database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// 初始化 Redis（addr 为空则跳过）
	if err := cache.Init(cfg.Redis); err != nil {
		logger.Error("Failed to init redis", "error", err)
		os.Exit(1)
	}
	defer cache.Close()

	r := api.SetupRouter(cfg.Server.Mode, cfg.Server.TrustedProxies, cfg.Server.AllowedOrigins)

	// 优雅关闭
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		logger.Info("Shutting down...")
		api.Shutdown() // 通知各模块收尾（如 hitcount flush 内存计数）
		cache.Close()
		database.Close()
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("Server starting", "addr", addr, "mode", cfg.Server.Mode)
	if err := r.Run(addr); err != nil {
		logger.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
