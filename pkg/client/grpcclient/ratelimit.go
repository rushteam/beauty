package grpcclient

import (
	"context"

	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newRateLimiter creates a UnaryClientInterceptor for client side rate limiting.
func newRateLimiter(limit float64, burst int) grpc.UnaryClientInterceptor {
	if burst == 0 {
		burst = int(limit)
	}
	limiter := rate.NewLimiter(rate.Limit(limit), burst)
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := limiter.Wait(ctx)
		if err != nil {
			return status.Error(codes.ResourceExhausted, err.Error())
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func WithRateLimiter(limit float64, burst int) Option {
	return func(c *Client) {
		c.unaryInterceptors = append(c.unaryInterceptors, newRateLimiter(limit, burst))
	}
}
