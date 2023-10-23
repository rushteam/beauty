package log

import (
	"log/slog"
)

// Debug ..
func Debug(msg string, args ...any) {
	slog.Debug(msg, args...)
}

// Info ..
func Info(msg string, args ...any) {
	slog.Info(msg, args...)
}

// Warn ..
func Warn(msg string, args ...any) {
	slog.Warn(msg, args...)
}

// Error ..
func Error(msg string, args ...any) {
	slog.Warn(msg, args...)
}
