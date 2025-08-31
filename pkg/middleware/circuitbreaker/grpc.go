package circuitbreaker

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor 返回一个 gRPC 一元服务器拦截器，用于熔断
func UnaryServerInterceptor(cb *CircuitBreaker) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return cb.Execute(func() (interface{}, error) {
			return handler(ctx, req)
		})
	}
}

// UnaryClientInterceptor 返回一个 gRPC 一元客户端拦截器，用于熔断
func UnaryClientInterceptor(cb *CircuitBreaker) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return cb.Call(func() error {
			return invoker(ctx, method, req, reply, cc, opts...)
		})
	}
}

// StreamServerInterceptor 返回一个 gRPC 流服务器拦截器，用于熔断
func StreamServerInterceptor(cb *CircuitBreaker) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return cb.Call(func() error {
			return handler(srv, ss)
		})
	}
}

// StreamClientInterceptor 返回一个 gRPC 流客户端拦截器，用于熔断
func StreamClientInterceptor(cb *CircuitBreaker) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		var clientStream grpc.ClientStream
		err := cb.Call(func() error {
			var err error
			clientStream, err = streamer(ctx, desc, cc, method, opts...)
			return err
		})
		return clientStream, err
	}
}

// IsCircuitBreakerError 检查错误是否为熔断器相关错误
func IsCircuitBreakerError(err error) bool {
	if err == ErrCircuitBreakerOpen || err == ErrTooManyRequests {
		return true
	}

	// 检查 gRPC 状态码
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.Unavailable &&
			(st.Message() == ErrCircuitBreakerOpen.Error() ||
				st.Message() == ErrTooManyRequests.Error())
	}

	return false
}

// WrapCircuitBreakerError 将熔断器错误包装为 gRPC 错误
func WrapCircuitBreakerError(err error) error {
	if err == ErrCircuitBreakerOpen || err == ErrTooManyRequests {
		return status.Error(codes.Unavailable, err.Error())
	}
	return err
}
