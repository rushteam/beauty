package accesslog

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/rushteam/beauty/pkg/middleware/requestid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// HTTPMiddleware 记录每个 HTTP 请求的 method、path、status、latency 和 request-id。
// 需放在 recovery 之后、业务 handler 之前，确保 panic 被兜住后仍能记录日志。
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		latency := time.Since(start)

		lvl := slog.LevelInfo
		if rw.code >= 500 {
			lvl = slog.LevelWarn
		}

		slog.Log(r.Context(), lvl, "access",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.code,
			"latency_ms", latency.Milliseconds(),
			"request_id", requestid.FromContext(r.Context()),
			"remote_addr", r.RemoteAddr,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.code = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

// UnaryServerInterceptor 记录每个 gRPC unary 调用的 method、code、latency。
func UnaryServerInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	latency := time.Since(start)

	code := codes.OK
	if err != nil {
		code = status.Code(err)
	}

	lvl := slog.LevelInfo
	if code != codes.OK && code != codes.Canceled {
		lvl = slog.LevelWarn
	}

	slog.Log(ctx, lvl, "grpc access",
		"method", info.FullMethod,
		"code", code.String(),
		"latency_ms", latency.Milliseconds(),
		"request_id", requestid.FromContext(ctx),
	)
	return resp, err
}

// StreamServerInterceptor 记录每个 gRPC stream 调用的 method、code、latency。
func StreamServerInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	err := handler(srv, ss)
	latency := time.Since(start)

	code := codes.OK
	if err != nil {
		code = status.Code(err)
	}

	lvl := slog.LevelInfo
	if code != codes.OK && code != codes.Canceled {
		lvl = slog.LevelWarn
	}

	slog.Log(ss.Context(), lvl, "grpc stream access",
		"method", info.FullMethod,
		"code", code.String(),
		"latency_ms", latency.Milliseconds(),
		"request_id", requestid.FromContext(ss.Context()),
	)
	return err
}
