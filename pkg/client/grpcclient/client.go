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
// （OTel 统计、keepalive、默认重试策略）。
//
// 空闲超时沿用 gRPC 默认（30 分钟）：进出 idle 会重建 resolver 与全部 SubConn，
// 过短会让每次空闲后的首个 RPC 都重新拉注册中心并握手。
// keepalive 不在无活跃流时发 ping（gRPC 默认）：默认配置的服务端（MinTime 5 分钟、
// 不允许无流 ping）会以 too_many_pings 断开空闲时仍在 ping 的连接。
func standardDialOpts() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    time.Second * 20,
			Timeout: time.Second * 10,
		}),
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
