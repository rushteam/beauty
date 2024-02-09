package logger

import (
	"log/slog"
)

var logger *slog.Logger

func init() {
	logger = slog.Default().WithGroup("beauty")
}

// Debug ..
func Debug(msg string, args ...any) {
	logger.Debug(msg, args...)
}

// Info ..
func Info(msg string, args ...any) {
	logger.Info(msg, args...)
}

// Warn ..
func Warn(msg string, args ...any) {
	logger.Warn(msg, args...)
}

// Error ..
func Error(msg string, args ...any) {
	logger.Warn(msg, args...)
}

// Sync ..
func Sync() error {
	return nil
}
