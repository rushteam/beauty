// Package redact 提供日志敏感数据脱敏:在 slog 中标记敏感属性,
// 通过 Logger 配置决定是否在日志输出中包含这些数据。
package redact

import (
	"context"
	"iter"
	"log/slog"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

type sensitiveData struct {
	inner any
}

func (s sensitiveData) LogValue() slog.Value {
	return slog.AnyValue(s.inner)
}

// SensitiveData 创建一个标记为敏感的 slog.Attr。
// 当 Logger.IncludeSensitive 为 false 时,该属性会被从日志中完全移除。
func SensitiveData(key string, value any) slog.Attr {
	return slog.Any(key, sensitiveData{inner: value})
}

// Logger 包装 slog.Logger,支持敏感数据脱敏。
type Logger struct {
	// Inner 是底层 slog.Logger。nil 时使用 slog.Default()。
	Inner *slog.Logger

	// IncludeSensitive 控制是否在日志中包含敏感数据。
	// false(默认):SensitiveData 标记的属性被完全移除。
	// true:SensitiveData 标记的属性正常输出。
	IncludeSensitive bool
}

func (l *Logger) base() *slog.Logger {
	if l != nil && l.Inner != nil {
		return l.Inner
	}
	return slog.Default()
}

func (l *Logger) includeSensitive() bool {
	return l != nil && l.IncludeSensitive
}

// Log 记录日志,根据 IncludeSensitive 过滤敏感属性。
func (l *Logger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if !l.includeSensitive() {
		args = filterSensitive(args)
	}
	l.base().Log(ctx, level, msg, args...)
}

// Debug 是 Log(ctx, slog.LevelDebug, ...) 的便捷方法。
func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, slog.LevelDebug, msg, args...)
}

// Info 是 Log(ctx, slog.LevelInfo, ...) 的便捷方法。
func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, slog.LevelInfo, msg, args...)
}

// Warn 是 Log(ctx, slog.LevelWarn, ...) 的便捷方法。
func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, slog.LevelWarn, msg, args...)
}

// Error 是 Log(ctx, slog.LevelError, ...) 的便捷方法。
func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, slog.LevelError, msg, args...)
}

// With 返回带有额外属性的新 Logger(继承 IncludeSensitive 设置)。
func (l *Logger) With(args ...any) *Logger {
	filtered := args
	if !l.includeSensitive() {
		filtered = filterSensitive(args)
	}
	nl := &Logger{Inner: l.base().With(filtered...)}
	if l != nil {
		nl.IncludeSensitive = l.IncludeSensitive
	}
	return nl
}

func filterSensitive(args []any) []any {
	out := make([]any, 0, len(args))
	for i := 0; i < len(args); {
		if attr, ok := args[i].(slog.Attr); ok {
			if _, isSensitive := attr.Value.Any().(sensitiveData); !isSensitive {
				out = append(out, attr)
			}
			i++
			continue
		}
		if i+1 < len(args) {
			if _, ok := asSensitiveKeyValue(args[i], args[i+1]); ok {
				i += 2
				continue
			}
			out = append(out, args[i], args[i+1])
			i += 2
			continue
		}
		out = append(out, args[i])
		i++
	}
	return out
}

func asSensitiveKeyValue(key, value any) (slog.Attr, bool) {
	keyStr, ok := key.(string)
	if !ok {
		return slog.Attr{}, false
	}
	if _, ok := value.(sensitiveData); ok {
		return slog.Any(keyStr, value), true
	}
	return slog.Attr{}, false
}

// RedactedLoggingMiddleware 是带敏感数据脱敏的日志中间件。
func RedactedLoggingMiddleware(logger *Logger) agent.AgentMiddleware {
	return func(next agent.AgentRunFunc) agent.AgentRunFunc {
		return func(ctx context.Context, req llm.Request, opts ...agent.Option) iter.Seq2[agent.Event, error] {
			if logger != nil {
				logger.Info(ctx, "agent.run.start",
					"model", req.Model,
					"messages", len(req.Messages),
					"tools", len(req.Tools),
					SensitiveData("messages_content", req.Messages),
				)
			}
			return func(yield func(agent.Event, error) bool) {
				var lastResp *llm.Response
				for ev, err := range next(ctx, req, opts...) {
					if err != nil {
						if logger != nil {
							logger.Error(ctx, "agent.run.error", "error", err.Error())
						}
						yield(ev, err)
						return
					}
					if ev.Type == agent.EventFinal {
						lastResp = ev.Response
					}
					if !yield(ev, nil) {
						return
					}
				}
				if logger != nil && lastResp != nil {
					logger.Info(ctx, "agent.run.done",
						"content_len", len(lastResp.Content),
						"input_tokens", lastResp.Usage.InputTokens,
						"output_tokens", lastResp.Usage.OutputTokens,
					)
				}
			}
		}
	}
}
