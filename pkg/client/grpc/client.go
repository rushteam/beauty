package grpcclient

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type Option func(c *Client)

func WithAddr(addr string) Option {
	return func(c *Client) {
		c.Addr = addr
	}
}

func WithBlock() Option {
	return func(c *Client) {
		// grpc.WithBlock() 已废弃，在 grpc.NewClient 中默认行为已改变
		// 新版本默认是非阻塞的，如果需要阻塞行为，可以在连接后手动等待状态
		// 这里保留函数以保持向后兼容性，但不添加任何选项
	}
}

func WithInsecure() Option {
	return func(c *Client) {
		c.DialOpts = append(c.DialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

func WithBalancingPolicy(policy string) Option {
	// {"loadBalancingConfig": [{"round_robin":{}}]}
	serverConfig := fmt.Sprintf(`{"loadBalancingPolicy":"%s"}`, policy)
	return func(c *Client) {
		// grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`),
		c.DialOpts = append(c.DialOpts, grpc.WithDefaultServiceConfig(serverConfig))
	}
}

func WithDefault() Option {
	return func(c *Client) {
		c.DialOpts = append(c.DialOpts,
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                time.Second * 20,
				Timeout:             time.Second * 10,
				PermitWithoutStream: true,
			}),
			grpc.WithIdleTimeout(time.Second*10),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
}

func WithDialOpts(opts ...grpc.DialOption) Option {
	return func(c *Client) {
		c.DialOpts = append(c.DialOpts, opts...)
	}
}

type Client struct {
	*grpc.ClientConn
	Addr              string
	DialOpts          []grpc.DialOption
	unaryInterceptors []grpc.UnaryClientInterceptor
}

func (c *Client) Close() error {
	if c.ClientConn != nil {
		return c.ClientConn.Close()
	}
	return nil
}

func New(ctx context.Context, opts ...Option) (*Client, error) {
	c := &Client{
		DialOpts: []grpc.DialOption{
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	c.DialOpts = append(c.DialOpts, grpc.WithChainUnaryInterceptor(c.unaryInterceptors...))

	// 使用 grpc.NewClient 替代废弃的
	conn, err := grpc.NewClient(c.Addr, c.DialOpts...)
	if err != nil {
		return nil, err
	}
	c.ClientConn = conn
	return c, nil
}
