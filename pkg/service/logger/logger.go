package logger

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

var logger *slog.Logger
var once sync.Once

func instance() {
	once.Do(func() {
		logger = slog.Default().WithGroup("beauty")
	})
}

// Debug ..
func Debug(msg string, args ...any) {
	instance()
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:]) // skip [Callers, Infof]
	r := slog.NewRecord(time.Now(), slog.LevelDebug, msg, pcs[0])
	r.Add(args...)
	_ = logger.Handler().Handle(context.Background(), r)
}

// Info ..
func Info(msg string, args ...any) {
	instance()
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:]) // skip [Callers, Infof]
	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, pcs[0])
	r.Add(args...)
	_ = logger.Handler().Handle(context.Background(), r)
}

// Warn ..
func Warn(msg string, args ...any) {
	instance()
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:]) // skip [Callers, Infof]
	r := slog.NewRecord(time.Now(), slog.LevelError, msg, pcs[0])
	r.Add(args...)
	_ = logger.Handler().Handle(context.Background(), r)

}

// Error ..
func Error(msg string, args ...any) {
	instance()
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:]) // skip [Callers, Infof]
	r := slog.NewRecord(time.Now(), slog.LevelError, msg, pcs[0])
	r.Add(args...)
	_ = logger.Handler().Handle(context.Background(), r)
}

// Sync ..
func Sync() error {
	return nil
}
