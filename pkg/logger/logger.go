package logger

import (
	"context"
	"log/slog"
)

var logger *slog.Logger

func init() {
	logger = slog.Default().WithGroup("beauty")
}

// Debug ..
func Debug(msg string, args ...any) {
	logger.Log(context.Background(), slog.LevelDebug, msg, args...)
}

// Info ..
func Info(msg string, args ...any) {
	logger.Log(context.Background(), slog.LevelInfo, msg, args...)
}

// Warn ..
func Warn(msg string, args ...any) {
	logger.Log(context.Background(), slog.LevelWarn, msg, args...)
}

// Error ..
func Error(msg string, args ...any) {
	logger.Log(context.Background(), slog.LevelError, msg, args...)
}

// Sync ..
func Sync() error {
	return nil
}
