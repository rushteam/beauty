// Package callbacks 把 pkg/callbacks 的切面接入 beauty 的 HTTP / gRPC 服务：
// 在每个请求前后触发已注册的 callbacks.Handler（全局或随 ctx 局部），
// 用于统一注入 trace / metrics / 日志，而无需改动业务 handler。
//
//	// 注册一个观测 handler
//	callbacks.AppendGlobalHandlers(myHandler)
//	// HTTP
//	webserver.New(addr, mux, webserver.WithMiddleware(cbmw.HTTPMiddleware()))
//	// gRPC
//	beauty.WithGrpcServer(addr, reg,
//	    grpcserver.WithGrpcServerUnaryInterceptor(cbmw.UnaryServerInterceptor()))
package callbacks

import (
	"context"
	"fmt"
	"net/http"

	cb "github.com/rushteam/beauty/pkg/callbacks"
	"google.golang.org/grpc"
)

// HTTPMiddleware 返回一个 HTTP 中间件，围绕每个请求触发 callbacks 切面：
// OnStart（请求进入）→ OnEnd（2xx-4xx）或 OnError（5xx）。
func HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := &cb.RunInfo{Name: r.Method + " " + r.URL.Path, Component: "http"}
			ctx := cb.OnStart(r.Context(), info, r)

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r.WithContext(ctx))

			if sw.status >= http.StatusInternalServerError {
				cb.OnError(ctx, info, fmt.Errorf("http status %d", sw.status))
			} else {
				cb.OnEnd(ctx, info, sw.status)
			}
		})
	}
}

// statusWriter 捕获响应状态码，同时通过 Unwrap 透传底层 ResponseWriter，
// 以便 http.ResponseController（Flush/Hijack 等）仍可工作。
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// UnaryServerInterceptor 返回一个 gRPC unary 拦截器，围绕每次调用触发 callbacks 切面。
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ri := &cb.RunInfo{Name: info.FullMethod, Component: "grpc"}
		ctx = cb.OnStart(ctx, ri, req)
		resp, err := handler(ctx, req)
		if err != nil {
			cb.OnError(ctx, ri, err)
		} else {
			cb.OnEnd(ctx, ri, resp)
		}
		return resp, err
	}
}

// StreamServerInterceptor 返回一个 gRPC stream 拦截器，围绕流的生命周期触发 callbacks 切面。
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ri := &cb.RunInfo{Name: info.FullMethod, Component: "grpc"}
		ctx := cb.OnStart(ss.Context(), ri, nil)
		err := handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
		if err != nil {
			cb.OnError(ctx, ri, err)
		} else {
			cb.OnEnd(ctx, ri, nil)
		}
		return err
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
