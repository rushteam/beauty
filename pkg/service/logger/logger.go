package logger

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

var logger *slog.Logger
var once sync.Once

func instance() {
	once.Do(func() {
		logger = slog.Default().WithGroup("beauty")
	})
}

func traceArgs(ctx context.Context) []any {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return nil
	}
	sc := span.SpanContext()
	return []any{
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	}
}

func handle(ctx context.Context, level slog.Level, msg string, args []any) {
	instance()
	if !logger.Handler().Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(traceArgs(ctx)...)
	r.Add(args...)
	_ = logger.Handler().Handle(ctx, r)
}

// Debug ..
func Debug(msg string, args ...any) {
	handle(context.Background(), slog.LevelDebug, msg, args)
}

// Info ..
func Info(msg string, args ...any) {
	handle(context.Background(), slog.LevelInfo, msg, args)
}

// Warn ..
func Warn(msg string, args ...any) {
	handle(context.Background(), slog.LevelWarn, msg, args)
}

// Error ..
func Error(msg string, args ...any) {
	handle(context.Background(), slog.LevelError, msg, args)
}

// DebugCtx logs at Debug level with trace context.
func DebugCtx(ctx context.Context, msg string, args ...any) {
	handle(ctx, slog.LevelDebug, msg, args)
}

// InfoCtx logs at Info level with trace context.
func InfoCtx(ctx context.Context, msg string, args ...any) {
	handle(ctx, slog.LevelInfo, msg, args)
}

// WarnCtx logs at Warn level with trace context.
func WarnCtx(ctx context.Context, msg string, args ...any) {
	handle(ctx, slog.LevelWarn, msg, args)
}

// ErrorCtx logs at Error level with trace context.
func ErrorCtx(ctx context.Context, msg string, args ...any) {
	handle(ctx, slog.LevelError, msg, args)
}

// Sync ..
func Sync() error {
	return nil
}
