package recovery

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"

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
		onPanic: defaultOnPanic,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func defaultOnPanic(_ context.Context, p any, stack []byte) {
	slog.Error("panic recovered", "panic", p, "stack", string(stack))
}

func UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	o := buildOptions(opts)
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				stack := debug.Stack()
				o.onPanic(ctx, p, stack)
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

func StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	o := buildOptions(opts)
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if p := recover(); p != nil {
				stack := debug.Stack()
				o.onPanic(ss.Context(), p, stack)
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(srv, ss)
	}
}

func HTTPMiddleware(opts ...Option) func(http.Handler) http.Handler {
	o := buildOptions(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					stack := debug.Stack()
					o.onPanic(r.Context(), p, stack)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
