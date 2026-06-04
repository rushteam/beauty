package errors

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// ToGRPC 将 *Status 转换为 gRPC status error，供 gRPC handler 直接返回。
func ToGRPC(s *Status) error {
	return grpcstatus.Error(codes.Code(s.code.GRPCCode()), s.message)
}

// FromGRPCError 尝试将 gRPC status error 还原为 *Status。
// 若输入不是 gRPC status error，返回 (nil, false)。
func FromGRPCError(err error) (*Status, bool) {
	if err == nil {
		return nil, false
	}
	st, ok := grpcstatus.FromError(err)
	if !ok {
		return nil, false
	}
	return New(grpcCodeToFramework(st.Code()), st.Message()), true
}

// GRPCUnaryServerInterceptor 将 handler 返回的 *Status 自动转换为 gRPC status error。
// 业务代码直接 return errors.NotFound("user not found")，无需手动调 ToGRPC。
func GRPCUnaryServerInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	resp, err := handler(ctx, req)
	if err != nil {
		if s, ok := FromError(err); ok {
			return resp, ToGRPC(s)
		}
	}
	return resp, err
}

// GRPCStreamServerInterceptor 与 GRPCUnaryServerInterceptor 对应的 stream 版本。
func GRPCStreamServerInterceptor(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := handler(srv, ss)
	if err != nil {
		if s, ok := FromError(err); ok {
			return ToGRPC(s)
		}
	}
	return err
}

// grpcCodeToFramework 将 gRPC code 反查为框架 Code。
func grpcCodeToFramework(c codes.Code) Code {
	for code, meta := range registry {
		if meta.grpcCode == uint32(c) {
			return code
		}
	}
	switch c {
	case codes.InvalidArgument:
		return CodeInvalidArgument
	case codes.NotFound:
		return CodeNotFound
	case codes.AlreadyExists:
		return CodeConflict
	case codes.PermissionDenied:
		return CodeForbidden
	case codes.Unauthenticated:
		return CodeUnauthenticated
	case codes.ResourceExhausted:
		return CodeTooManyRequests
	case codes.Unimplemented:
		return CodeUnimplemented
	case codes.Unavailable:
		return CodeUnavailable
	case codes.DeadlineExceeded:
		return CodeDeadline
	case codes.FailedPrecondition:
		return CodeFailedPrecondition
	default:
		return CodeInternal
	}
}
