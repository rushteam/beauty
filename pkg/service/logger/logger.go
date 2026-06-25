package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

var logger *slog.Logger
var once sync.Once
var levelVar = new(slog.LevelVar) // default: slog.LevelInfo (0)

func instance() {
	once.Do(func() {
		h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})
		logger = slog.New(h).WithGroup("beauty")
	})
}

// SetLevel dynamically sets the global log level; takes effect immediately without restart.
func SetLevel(level slog.Level) {
	levelVar.Set(level)
}

// GetLevel returns the current log level.
func GetLevel() slog.Level {
	return levelVar.Level()
}

// SetLevelByName sets the log level by name: "debug"/"info"/"warn"/"error" (case-insensitive).
// Returns an error for unrecognized names without changing the current level.
func SetLevelByName(name string) error {
	switch strings.ToLower(name) {
	case "debug":
		levelVar.Set(slog.LevelDebug)
	case "info":
		levelVar.Set(slog.LevelInfo)
	case "warn":
		levelVar.Set(slog.LevelWarn)
	case "error":
		levelVar.Set(slog.LevelError)
	default:
		return fmt.Errorf("unknown log level %q: must be debug, info, warn or error", name)
	}
	return nil
}

// LevelHandler returns an http.Handler that exposes dynamic log-level control.
//
//	GET  /loglevel  -> {"level":"info"}
//	PUT  /loglevel  -> body {"level":"debug"}, returns 200 {"level":"debug"} or 400 on error
func LevelHandler() http.Handler {
	type payload struct {
		Level string `json:"level"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(payload{Level: levelVar.Level().String()})
		case http.MethodPut:
			var p payload
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			if err := SetLevelByName(p.Level); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(payload{Level: levelVar.Level().String()})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// TraceHandler 包装一个 slog.Handler，在每条日志记录上自动追加当前
// OpenTelemetry span 的 trace_id / span_id（取自记录的 context）。
// 仅当 context 中存在有效 span 时才追加，因此需用 *Context 系列方法记录日志：
//
//	base := slog.NewJSONHandler(os.Stdout, opts)
//	slog.SetDefault(slog.New(logger.NewTraceHandler(base)))
//	slog.InfoContext(ctx, "handled request") // 自动带上 trace_id/span_id
func NewTraceHandler(h slog.Handler) slog.Handler {
	return &traceHandler{Handler: h}
}

type traceHandler struct {
	slog.Handler
}

func (t *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if args := traceArgs(ctx); len(args) > 0 {
		r.Add(args...)
	}
	return t.Handler.Handle(ctx, r)
}

func (t *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: t.Handler.WithAttrs(attrs)}
}

func (t *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: t.Handler.WithGroup(name)}
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
