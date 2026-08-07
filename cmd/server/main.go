package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/database"
	_ "github.com/thun888/apibox/internal/api/modules" // 匿名导入，触发所有 API 模块的 init() 执行
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 初始化数据库（自动迁移在 Init 内部根据配置触发）
	if err := database.Init(cfg.Database); err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer database.Close()

	r := api.SetupRouter(cfg.Server.Mode, cfg.Server.TrustedProxies)

	// 优雅关闭
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down...")
		database.Close()
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("Server starting on %s (mode: %s)", addr, cfg.Server.Mode)
	if err := r.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
