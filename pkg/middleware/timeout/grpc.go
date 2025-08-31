package timeout

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor 返回一个 gRPC 一元服务器拦截器，用于超时控制
func UnaryServerInterceptor(tc *TimeoutController) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return tc.ExecuteWithResult(ctx, func(ctx context.Context) (interface{}, error) {
			return handler(ctx, req)
		})
	}
}

// UnaryClientInterceptor 返回一个 gRPC 一元客户端拦截器，用于超时控制
func UnaryClientInterceptor(tc *TimeoutController) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return tc.Execute(ctx, func(ctx context.Context) error {
			return invoker(ctx, method, req, reply, cc, opts...)
		})
	}
}

// StreamServerInterceptor 返回一个 gRPC 流服务器拦截器，用于超时控制
func StreamServerInterceptor(tc *TimeoutController) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return tc.Execute(ss.Context(), func(ctx context.Context) error {
			// 为流创建一个包装的服务器流，使用超时上下文
			wrappedStream := &timeoutServerStream{
				ServerStream: ss,
				ctx:          ctx,
			}
			return handler(srv, wrappedStream)
		})
	}
}

// StreamClientInterceptor 返回一个 gRPC 流客户端拦截器，用于超时控制
func StreamClientInterceptor(tc *TimeoutController) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		var clientStream grpc.ClientStream
		err := tc.Execute(ctx, func(ctx context.Context) error {
			var err error
			clientStream, err = streamer(ctx, desc, cc, method, opts...)
			return err
		})
		return clientStream, err
	}
}

// timeoutServerStream 包装 grpc.ServerStream 以支持超时上下文
type timeoutServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *timeoutServerStream) Context() context.Context {
	return s.ctx
}

// IsTimeoutError 检查错误是否为超时相关错误
func IsTimeoutError(err error) bool {
	if err == ErrTimeout || err == ErrTimeoutCanceled {
		return true
	}

	// 检查 gRPC 状态码
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.DeadlineExceeded ||
			st.Code() == codes.Canceled ||
			(st.Code() == codes.Internal &&
				(st.Message() == ErrTimeout.Error() ||
					st.Message() == ErrTimeoutCanceled.Error()))
	}

	return false
}

// WrapTimeoutError 将超时错误包装为 gRPC 错误
func WrapTimeoutError(err error) error {
	if err == ErrTimeout {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	if err == ErrTimeoutCanceled {
		return status.Error(codes.Canceled, err.Error())
	}
	return err
}
