package recovery

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"

	apperrors "github.com/rushteam/beauty/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type OnPanicFunc func(ctx context.Context, p any, stack []byte)

type options struct {
	onPanic OnPanicFunc
}

type Option func(*options)

func WithOnPanic(fn OnPanicFunc) Option {
	return func(o *options) {
		o.onPanic = fn
	}
}

func buildOptions(opts []Option) *options {
	o := &options{
		onPanic: func(_ context.Context, p any, stack []byte) {
			slog.Error("panic recovered", "panic", p, "stack", string(stack))
		},
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// UnaryServerInterceptor gRPC unary 拦截器：兜底 panic + 将 *errors.Status 转为 gRPC status。
func UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	o := buildOptions(opts)
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				o.onPanic(ctx, p, debug.Stack())
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		resp, err = handler(ctx, req)
		if err != nil {
			if s, ok := apperrors.FromError(err); ok {
				err = apperrors.ToGRPC(s)
			}
		}
		return resp, err
	}
}

// StreamServerInterceptor gRPC stream 拦截器：兜底 panic + 将 *errors.Status 转为 gRPC status。
func StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	o := buildOptions(opts)
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if p := recover(); p != nil {
				o.onPanic(ss.Context(), p, debug.Stack())
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		err = handler(srv, ss)
		if err != nil {
			if s, ok := apperrors.FromError(err); ok {
				err = apperrors.ToGRPC(s)
			}
		}
		return err
	}
}

// HTTPMiddleware HTTP 中间件：兜底 panic + 将 *errors.Status 写为结构化 JSON 响应。
func HTTPMiddleware(opts ...Option) func(http.Handler) http.Handler {
	o := buildOptions(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					o.onPanic(r.Context(), p, debug.Stack())
					apperrors.WriteHTTP(w, apperrors.Internal("internal server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
