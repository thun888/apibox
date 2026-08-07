package utils

import (
	"log/slog"
	"os"
)

// NewModuleLogger 为指定模块创建一个带模块名的 slog.Logger。
// 所有通过该 logger 输出的日志会自动带上 "module=<name>" 属性。
//
// 用法示例：
//
//	var log = utils.NewModuleLogger("mymodule")
//	log.Info("started", "port", 8080)
//	// 输出: time=... level=INFO msg=started module=mymodule port=8080
func NewModuleLogger(moduleName string) *slog.Logger {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	return slog.New(handler).With("module", moduleName)
}
