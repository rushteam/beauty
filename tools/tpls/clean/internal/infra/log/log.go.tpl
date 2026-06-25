// Package log 是 Ring 4（基础设施）：日志构建。
package log

import (
	"log/slog"
	"os"

	beautylog "github.com/rushteam/beauty/pkg/service/logger"
	"{{.ImportPath}}internal/infra/config"
)

// New 构建带 trace 关联的 slog.Logger。
// 通过 beautylog.NewTraceHandler 包装：使用 slog.*Context 方法记录日志时
// 会自动带上当前 span 的 trace_id / span_id。
func New(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Log.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	var h slog.Handler
	if cfg.Log.Format == "json" {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}
	return slog.New(beautylog.NewTraceHandler(h))
}
