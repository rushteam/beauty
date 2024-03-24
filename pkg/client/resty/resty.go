package resty

import (
	"github.com/dubonzi/otelresty"
	"github.com/go-resty/resty/v2"
)

type Client struct {
	*resty.Client
}

func New() *Client {
	cli := resty.New()
	opts := []otelresty.Option{otelresty.WithTracerName("beauty-resty")}
	otelresty.TraceClient(cli, opts...)
	return &Client{
		Client: cli,
	}
}
