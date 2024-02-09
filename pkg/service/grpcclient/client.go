package grpcclient

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Option func(c *Client)

func WithConfig(config Config) Option {
	return func(c *Client) {
		c.Addr = config.Addr
		c.DialOpts = append(c.DialOpts, grpc.WithBlock())
	}
}

func WithAddr(addr string) Option {
	return func(c *Client) {
		c.DialOpts = append(c.DialOpts, grpc.WithBlock())
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
	client := &Client{
		Addr: ":58080",
		DialOpts: []grpc.DialOption{
			grpc.WithBlock(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	}
	for _, opt := range opts {
		opt(client)
	}
	conn, err := grpc.DialContext(context.Background(), client.Addr, client.DialOpts...)
	if err != nil {
		return &Client{}, err
	}
	client.ClientConn = conn
	return client, nil
}
