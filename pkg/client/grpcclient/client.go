package grpcclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type Option func(c *Client)

func WithConfig(config Config) Option {
	return func(c *Client) {
		c.Addr = config.Addr
		// c.DialOpts = append(c.DialOpts, grpc.WithBlock())
	}
}

// func WithDiscover(addr discover.Resolver) Option {
// 	return func(c *Client) {
// 		// c.Addr = config.Addr
// 	}
// }

func WithAddr(addr string) Option {
	return func(c *Client) {
		c.Addr = addr
	}
}

func WithBlock() Option {
	return func(c *Client) {
		c.DialOpts = append(c.DialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

func WithInsecure() Option {
	return func(c *Client) {
		c.DialOpts = append(c.DialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

type Config struct {
	Addr string
}

type Client struct {
	*grpc.ClientConn
	Addr     string
	DialOpts []grpc.DialOption
}

func (c *Client) Close() {
	c.ClientConn.Close()
}

func New(opts ...Option) (*Client, error) {
	c := &Client{
		DialOpts: []grpc.DialOption{
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                time.Second * 20,
				Timeout:             time.Second * 10,
				PermitWithoutStream: true,
			}),
			grpc.WithIdleTimeout(time.Second * 10),
			// grpc.WithBlock(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	conn, err := grpc.DialContext(context.Background(), c.Addr, c.DialOpts...)
	if err != nil {
		return &Client{}, err
	}
	c.ClientConn = conn
	return c, nil
}
