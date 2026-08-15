// Package subscription 提供 GraphQL subscription 的 WebSocket 和 SSE 双传输实现。
// WebSocket 实现兼容 graphql-ws (Apollo) 子协议;SSE 适合单向推送场景。
package subscription

import (
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler/transport"
)

// WSOption 配置 WebSocket transport。
type WSOption func(*wsConfig)

type wsConfig struct {
	keepAlive        time.Duration
	pingInterval     time.Duration
	initTimeout      time.Duration
	closeGracePeriod time.Duration
}

// WithKeepAlive 设置 keep-alive 间隔(默认 30s)。
func WithKeepAlive(d time.Duration) WSOption {
	return func(c *wsConfig) { c.keepAlive = d }
}

// WithPingInterval 设置 ping 间隔(默认 20s)。
func WithPingInterval(d time.Duration) WSOption {
	return func(c *wsConfig) { c.pingInterval = d }
}

// WithInitTimeout 设置连接初始化超时(默认 15s)。
func WithInitTimeout(d time.Duration) WSOption {
	return func(c *wsConfig) { c.initTimeout = d }
}

// WSTransport 创建 WebSocket subscription transport (graphql-ws 子协议)。
func WSTransport(opts ...WSOption) graphql.Transport {
	cfg := wsConfig{
		keepAlive:    30 * time.Second,
		pingInterval: 20 * time.Second,
		initTimeout:  15 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &transport.Websocket{
		KeepAlivePingInterval: cfg.pingInterval,
		InitTimeout:           cfg.initTimeout,
	}
}

// SSEOption 配置 SSE transport。
type SSEOption func(*sseConfig)

type sseConfig struct{}

// SSETransport 创建 SSE subscription transport。
// SSE 适合单向推送,不需要 WebSocket 升级,兼容 HTTP/2 多路复用。
func SSETransport(opts ...SSEOption) graphql.Transport {
	return transport.SSE{}
}
