// Package grpcauth 提供轻量级 gRPC 静态 Bearer Token 认证拦截器。
// 适用于内部服务间通信的简单预共享 token 校验场景。
// 如需完整的认证/授权流程（JWT 解析、RBAC 等），请使用 pkg/middleware/auth。
package grpcauth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor 返回校验静态 Bearer Token 的一元拦截器。
// token 为期望的令牌值（不含 "Bearer " 前缀）。
// required 为 true 时缺少 token 返回 Unauthenticated；为 false 时放行。
func UnaryServerInterceptor(token string, required bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := verifyBearer(ctx, token, required); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor 返回校验静态 Bearer Token 的流拦截器。
func StreamServerInterceptor(token string, required bool) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := verifyBearer(ss.Context(), token, required); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func verifyBearer(ctx context.Context, expected string, required bool) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		if required {
			return status.Error(codes.Unauthenticated, "missing metadata")
		}
		return nil
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		if required {
			return status.Error(codes.Unauthenticated, "missing authorization header")
		}
		return nil
	}
	const prefix = "Bearer "
	v := vals[0]
	if !strings.HasPrefix(v, prefix) {
		return status.Error(codes.Unauthenticated, "invalid authorization format")
	}
	if v[len(prefix):] != expected {
		return status.Error(codes.Unauthenticated, "invalid bearer token")
	}
	return nil
}
