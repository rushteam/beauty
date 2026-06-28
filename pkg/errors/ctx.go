package errors

import (
	"context"

	"github.com/rushteam/beauty/pkg/ctxkey"
)

var errorSinkKey = ctxkey.New[*errorSink]()

type errorSink struct {
	err error
}

func withErrorSink(ctx context.Context) context.Context {
	return ctxkey.With(ctx, errorSinkKey, &errorSink{})
}

// SetError 将错误写入 ctx 的 errorSink，供 HTTPMiddlewareErrorHandler 读取。
// 若 ctx 没有 sink（未经过 HTTPMiddlewareErrorHandler 包装），调用是无操作。
func SetError(ctx context.Context, err error) {
	if s, ok := ctxkey.Get(ctx, errorSinkKey); ok {
		s.err = err
	}
}

func getError(ctx context.Context) error {
	if s, ok := ctxkey.Get(ctx, errorSinkKey); ok {
		return s.err
	}
	return nil
}
