package ratelimit

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor 返回一个 gRPC 一元服务器拦截器，用于限流
func UnaryServerInterceptor(rl *RateLimitMiddleware) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// 构建元数据
		metadata := buildGRPCMetadata(ctx, info.FullMethod)

		// 执行限流检查
		err := rl.Allow(ctx, metadata)
		if err != nil {
			return nil, wrapRateLimitError(err)
		}

		return handler(ctx, req)
	}
}

// UnaryServerWaitInterceptor 返回等待型一元服务器拦截器（会等待而不是直接拒绝）
func UnaryServerWaitInterceptor(rl *RateLimitMiddleware) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// 构建元数据
		metadata := buildGRPCMetadata(ctx, info.FullMethod)

		// 等待限流通过
		err := rl.Wait(ctx, metadata)
		if err != nil {
			return nil, wrapRateLimitError(err)
		}

		return handler(ctx, req)
	}
}

// UnaryClientInterceptor 返回一个 gRPC 一元客户端拦截器，用于限流
func UnaryClientInterceptor(rl *RateLimitMiddleware) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// 构建元数据
		metadata := buildGRPCMetadata(ctx, method)

		// 执行限流检查
		err := rl.Allow(ctx, metadata)
		if err != nil {
			return wrapRateLimitError(err)
		}

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamServerInterceptor 返回一个 gRPC 流服务器拦截器，用于限流
func StreamServerInterceptor(rl *RateLimitMiddleware) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// 构建元数据
		metadata := buildGRPCMetadata(ss.Context(), info.FullMethod)

		// 执行限流检查
		err := rl.Allow(ss.Context(), metadata)
		if err != nil {
			return wrapRateLimitError(err)
		}

		return handler(srv, ss)
	}
}

// StreamClientInterceptor 返回一个 gRPC 流客户端拦截器，用于限流
func StreamClientInterceptor(rl *RateLimitMiddleware) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// 构建元数据
		metadata := buildGRPCMetadata(ctx, method)

		// 执行限流检查
		err := rl.Allow(ctx, metadata)
		if err != nil {
			return nil, wrapRateLimitError(err)
		}

		return streamer(ctx, desc, cc, method, opts...)
	}
}

// buildGRPCMetadata 构建 gRPC 请求元数据
func buildGRPCMetadata(ctx context.Context, method string) map[string]interface{} {
	md := make(map[string]interface{})

	// 添加方法名
	md["method"] = method
	md["path"] = method

	// 添加 gRPC metadata
	if grpcMD, ok := metadata.FromIncomingContext(ctx); ok {
		grpcMetadata := make(map[string][]string)
		for k, v := range grpcMD {
			grpcMetadata[k] = v
		}
		md["grpc_metadata"] = grpcMetadata
	}

	// 添加 peer 信息
	if p, ok := peer.FromContext(ctx); ok {
		md["peer_addr"] = p.Addr.String()
	}

	// 添加用户信息（如果存在）
	if user := ctx.Value("user"); user != nil {
		md["user"] = user
	}

	return md
}

// wrapRateLimitError 将限流错误包装为 gRPC 错误
func wrapRateLimitError(err error) error {
	if err == ErrRateLimitExceeded {
		return status.Error(codes.ResourceExhausted, err.Error())
	}
	if err == context.DeadlineExceeded {
		return status.Error(codes.DeadlineExceeded, "rate limit wait timeout")
	}
	return status.Error(codes.Internal, err.Error())
}

// IsGRPCRateLimitError 检查错误是否为 gRPC 限流相关错误
func IsGRPCRateLimitError(err error) bool {
	if IsRateLimitError(err) {
		return true
	}

	// 检查 gRPC 状态码
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.ResourceExhausted &&
			st.Message() == ErrRateLimitExceeded.Error()
	}

	return false
}
