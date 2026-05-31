// Package propagation 提供 MD 在 HTTP / gRPC 传输层的注入与提取实现。
//
// 仅透传以 "x-" 开头的 header/metadata key，避免将 Content-Type、
// Authorization 等控制头意外带入业务透传链路。
package propagation

import (
	"context"
	"net/http"
	"strings"

	"github.com/rushteam/beauty/pkg/metadata"
	"google.golang.org/grpc"
	grpcmd "google.golang.org/grpc/metadata"
)

// prefix 约定：只透传以此前缀开头的 key。
const prefix = "x-"

func isPassthrough(key string) bool {
	return strings.HasPrefix(strings.ToLower(key), prefix)
}

// ---- HTTP ---------------------------------------------------------------

// HTTPExtract 从 HTTP Request Header 中提取所有 x-* 键并返回 MD。
func HTTPExtract(r *http.Request) metadata.MD {
	md := metadata.New()
	for key, vals := range r.Header {
		k := strings.ToLower(key)
		if isPassthrough(k) && len(vals) > 0 {
			md.Set(k, vals[0])
		}
	}
	return md
}

// HTTPInject 将 MD 中所有 x-* 键写入 HTTP Header（规范化为 Header-Case）。
func HTTPInject(md metadata.MD, h http.Header) {
	for k, v := range md {
		if isPassthrough(k) {
			h.Set(k, v)
		}
	}
}

// HTTPServerMiddleware 从入站请求提取 MD 并注入 context，同时将 MD 写回响应 Header
// 以便调用方感知（如链路追踪系统读取 x-request-id）。
func HTTPServerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		md := HTTPExtract(r)
		ctx := metadata.NewContext(r.Context(), md)
		// 将透传字段回写到响应 header，方便客户端读取
		HTTPInject(md, w.Header())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// HTTPClientInject 从 ctx 中取出 MD，将 x-* 键注入到 HTTP Request Header，
// 用于 HTTP 客户端发出请求时透传。
func HTTPClientInject(ctx context.Context, r *http.Request) {
	md := metadata.FromContext(ctx)
	if len(md) == 0 {
		return
	}
	HTTPInject(md, r.Header)
}

// ---- gRPC server --------------------------------------------------------

// GRPCExtract 从 gRPC incoming metadata 中提取所有 x-* 键并返回 MD。
func GRPCExtract(ctx context.Context) metadata.MD {
	md := metadata.New()
	incoming, ok := grpcmd.FromIncomingContext(ctx)
	if !ok {
		return md
	}
	for k, vals := range incoming {
		if isPassthrough(k) && len(vals) > 0 {
			md.Set(k, vals[0])
		}
	}
	return md
}

// GRPCServerUnaryInterceptor 从入站 gRPC metadata 提取 MD 并注入 context。
func GRPCServerUnaryInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	md := GRPCExtract(ctx)
	if len(md) > 0 {
		ctx = metadata.NewContext(ctx, md)
	}
	return handler(ctx, req)
}

// GRPCServerStreamInterceptor 从入站 gRPC metadata 提取 MD 并注入 context。
func GRPCServerStreamInterceptor(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	md := GRPCExtract(ss.Context())
	if len(md) == 0 {
		return handler(srv, ss)
	}
	ctx := metadata.NewContext(ss.Context(), md)
	return handler(srv, &wrappedStream{ss, ctx})
}

// ---- gRPC client --------------------------------------------------------

// GRPCClientInject 从 ctx 中取出 MD，将 x-* 键追加到 gRPC outgoing metadata，
// 用于 gRPC 客户端发出请求时透传。
//
// 使用方式：
//
//	ctx = propagation.GRPCClientInject(ctx)
//	resp, err := client.MyMethod(ctx, req)
func GRPCClientInject(ctx context.Context) context.Context {
	md := metadata.FromContext(ctx)
	if len(md) == 0 {
		return ctx
	}
	pairs := make([]string, 0, len(md)*2)
	for k, v := range md {
		if isPassthrough(k) {
			pairs = append(pairs, k, v)
		}
	}
	if len(pairs) == 0 {
		return ctx
	}
	outgoing := grpcmd.Pairs(pairs...)
	// 与已有 outgoing metadata 合并，不覆盖
	if existing, ok := grpcmd.FromOutgoingContext(ctx); ok {
		outgoing = grpcmd.Join(existing, outgoing)
	}
	return grpcmd.NewOutgoingContext(ctx, outgoing)
}

// GRPCClientUnaryInterceptor 自动将 context 中的 MD 注入到每次 gRPC 调用的 outgoing metadata。
// 注册到 grpc.Dial 的 WithChainUnaryInterceptor 即可无感透传。
func GRPCClientUnaryInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return invoker(GRPCClientInject(ctx), method, req, reply, cc, opts...)
}

// GRPCClientStreamInterceptor 自动将 context 中的 MD 注入到每次 gRPC stream 的 outgoing metadata。
func GRPCClientStreamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return streamer(GRPCClientInject(ctx), desc, cc, method, opts...)
}

// ---- internal -----------------------------------------------------------

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
