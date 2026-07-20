package grpcclient

import (
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

type directOption func(c *directClient)

func withDirectAddr(addr string) directOption {
	return func(c *directClient) {
		c.addr = addr
	}
}

func withDirectBalancingPolicy(policy string) directOption {
	serverConfig := fmt.Sprintf(`{"loadBalancingPolicy":"%s"}`, policy)
	return func(c *directClient) {
		c.dialOpts = append(c.dialOpts, grpc.WithDefaultServiceConfig(serverConfig))
	}
}

func withDirectDialOpts(opts ...grpc.DialOption) directOption {
	return func(c *directClient) {
		c.dialOpts = append(c.dialOpts, opts...)
	}
}

type directClient struct {
	*grpc.ClientConn
	addr     string
	dialOpts []grpc.DialOption
}

func (c *directClient) Close() error {
	if c.ClientConn != nil {
		return c.ClientConn.Close()
	}
	return nil
}

// standardDialOpts 返回所有拨号模式共用的基础 gRPC 选项
// （OTel 统计、keepalive、空闲超时、默认重试策略）。
func standardDialOpts() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                time.Second * 20,
			Timeout:             time.Second * 10,
			PermitWithoutStream: true,
		}),
		grpc.WithIdleTimeout(time.Second * 10),
		grpc.WithDefaultServiceConfig(DefaultRetryPolicy().serviceConfig()),
	}
}

func newDirectClient(opts ...directOption) (*directClient, error) {
	c := &directClient{
		dialOpts: standardDialOpts(),
	}
	for _, opt := range opts {
		opt(c)
	}
	conn, err := grpc.NewClient(c.addr, c.dialOpts...)
	if err != nil {
		return nil, err
	}
	c.ClientConn = conn
	return c, nil
}
