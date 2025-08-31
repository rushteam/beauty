package auth

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor 返回一个 gRPC 一元服务器拦截器，用于认证
func UnaryServerInterceptor(auth *AuthMiddleware) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// 检查是否跳过认证
		if auth.ShouldSkip(info.FullMethod) {
			auth.recordSkipped()
			return handler(ctx, req)
		}

		// 构建元数据
		metadata := buildGRPCMetadata(ctx, info.FullMethod)

		// 执行认证
		user, err := auth.Authenticate(ctx, metadata)
		if err != nil {
			return nil, wrapAuthError(err)
		}

		// 执行授权（如果配置了授权器）
		if err := auth.Authorize(ctx, user, info.FullMethod, "call"); err != nil {
			return nil, wrapAuthError(err)
		}

		// 将用户信息添加到上下文
		ctx = context.WithValue(ctx, "user", user)

		// 调用成功回调
		if auth.onAuthSuccess != nil {
			auth.onAuthSuccess(ctx, user)
		}

		return handler(ctx, req)
	}
}

// UnaryClientInterceptor 返回一个 gRPC 一元客户端拦截器，用于认证
func UnaryClientInterceptor(auth *AuthMiddleware) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// 构建元数据
		metadata := buildGRPCMetadata(ctx, method)

		// 执行认证
		user, err := auth.Authenticate(ctx, metadata)
		if err != nil {
			return wrapAuthError(err)
		}

		// 将用户信息添加到上下文
		ctx = context.WithValue(ctx, "user", user)

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamServerInterceptor 返回一个 gRPC 流服务器拦截器，用于认证
func StreamServerInterceptor(auth *AuthMiddleware) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// 检查是否跳过认证
		if auth.ShouldSkip(info.FullMethod) {
			auth.recordSkipped()
			return handler(srv, ss)
		}

		// 构建元数据
		metadata := buildGRPCMetadata(ss.Context(), info.FullMethod)

		// 执行认证
		user, err := auth.Authenticate(ss.Context(), metadata)
		if err != nil {
			return wrapAuthError(err)
		}

		// 执行授权（如果配置了授权器）
		if err := auth.Authorize(ss.Context(), user, info.FullMethod, "stream"); err != nil {
			return wrapAuthError(err)
		}

		// 创建包装的流，包含用户信息
		wrappedStream := &authServerStream{
			ServerStream: ss,
			ctx:          context.WithValue(ss.Context(), "user", user),
		}

		// 调用成功回调
		if auth.onAuthSuccess != nil {
			auth.onAuthSuccess(wrappedStream.ctx, user)
		}

		return handler(srv, wrappedStream)
	}
}

// StreamClientInterceptor 返回一个 gRPC 流客户端拦截器，用于认证
func StreamClientInterceptor(auth *AuthMiddleware) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// 构建元数据
		metadata := buildGRPCMetadata(ctx, method)

		// 执行认证
		user, err := auth.Authenticate(ctx, metadata)
		if err != nil {
			return nil, wrapAuthError(err)
		}

		// 将用户信息添加到上下文
		ctx = context.WithValue(ctx, "user", user)

		return streamer(ctx, desc, cc, method, opts...)
	}
}

// authServerStream 包装 grpc.ServerStream 以支持认证上下文
type authServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authServerStream) Context() context.Context {
	return s.ctx
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
		md["remote_addr"] = p.Addr.String()
	}

	// 添加用户信息（如果存在）
	if user := ctx.Value("user"); user != nil {
		md["user"] = user
	}

	return md
}

// wrapAuthError 将认证错误包装为 gRPC 错误
func wrapAuthError(err error) error {
	if err == ErrUnauthorized || err == ErrInvalidToken || err == ErrTokenExpired {
		return status.Error(codes.Unauthenticated, err.Error())
	}
	if err == ErrForbidden {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

// IsGRPCAuthError 检查错误是否为 gRPC 认证相关错误
func IsGRPCAuthError(err error) bool {
	if IsAuthError(err) {
		return true
	}

	// 检查 gRPC 状态码
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.Unauthenticated || st.Code() == codes.PermissionDenied
	}

	return false
}

// GetUserFromGRPCContext 从 gRPC 上下文中获取用户信息
func GetUserFromGRPCContext(ctx context.Context) (User, bool) {
	return GetUserFromContext(ctx)
}

// RequireGRPCAuth 创建需要认证的 gRPC 拦截器
func RequireGRPCAuth(auth *AuthMiddleware) grpc.UnaryServerInterceptor {
	return UnaryServerInterceptor(auth)
}

// OptionalGRPCAuth 创建可选认证的 gRPC 拦截器（认证失败不会阻止请求）
func OptionalGRPCAuth(auth *AuthMiddleware) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// 构建元数据
		metadata := buildGRPCMetadata(ctx, info.FullMethod)

		// 尝试认证，但不阻止请求
		user, err := auth.Authenticate(ctx, metadata)
		if err == nil {
			// 认证成功，将用户信息添加到上下文
			ctx = context.WithValue(ctx, "user", user)

			if auth.onAuthSuccess != nil {
				auth.onAuthSuccess(ctx, user)
			}
		}
		// 认证失败也继续处理请求，不设置用户信息

		return handler(ctx, req)
	}
}
