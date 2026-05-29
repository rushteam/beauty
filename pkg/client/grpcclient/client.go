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

func newDirectClient(opts ...directOption) (*directClient, error) {
	c := &directClient{
		dialOpts: []grpc.DialOption{
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                time.Second * 20,
				Timeout:             time.Second * 10,
				PermitWithoutStream: true,
			}),
			grpc.WithIdleTimeout(time.Second * 10),
		},
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
