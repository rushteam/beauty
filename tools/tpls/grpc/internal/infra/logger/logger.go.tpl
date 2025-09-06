package logger

import (
	"io"
	"log/slog"
	"os"

	"{{.ImportPath}}internal/infra/conf"
)

// New 创建新的日志记录器
func New(cfg *conf.Log) *slog.Logger {
	var output io.Writer = os.Stdout
	if cfg.Output != "stdout" {
		// 这里可以添加文件输出逻辑
		output = os.Stdout
	}

	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(output, &slog.HandlerOptions{
			Level: level,
		})
	} else {
		handler = slog.NewTextHandler(output, &slog.HandlerOptions{
			Level: level,
		})
	}

	return slog.New(handler)
}
