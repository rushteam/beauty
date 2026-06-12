// Package ws 基于 github.com/coder/websocket 提供 WebSocket 的轻量封装：
// 自动完成握手升级、统一关闭语义，并把 *http.Request 透传给业务，
// 便于读取 query / header / 子协议 / 鉴权信息。
//
// 注意：WebSocket 升级依赖 http.Hijacker。otelhttp 会透传 Hijacker，可正常使用；
// 但不要给 WebSocket 路由套 compress 中间件（其 ResponseWriter 不直接实现 Hijacker）。
package ws

import (
	"context"
	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// MessageType 表示消息类型（文本 / 二进制）。
type MessageType = websocket.MessageType

const (
	// Text UTF-8 文本消息。
	Text = websocket.MessageText
	// Binary 二进制消息。
	Binary = websocket.MessageBinary
)

// StatusCode 是 WebSocket 关闭状态码。
type StatusCode = websocket.StatusCode

const (
	StatusNormalClosure = websocket.StatusNormalClosure
	StatusGoingAway     = websocket.StatusGoingAway
	StatusInternalError = websocket.StatusInternalError
)

// Conn 是对 websocket 连接的薄封装。
type Conn struct {
	raw *websocket.Conn
}

// Read 读取一条消息，返回类型与内容。连接关闭或 ctx 取消时返回错误。
func (c *Conn) Read(ctx context.Context) (MessageType, []byte, error) {
	return c.raw.Read(ctx)
}

// Write 写入一条指定类型的消息。
func (c *Conn) Write(ctx context.Context, typ MessageType, data []byte) error {
	return c.raw.Write(ctx, typ, data)
}

// WriteText 写入一条文本消息。
func (c *Conn) WriteText(ctx context.Context, s string) error {
	return c.raw.Write(ctx, Text, []byte(s))
}

// WriteBinary 写入一条二进制消息。
func (c *Conn) WriteBinary(ctx context.Context, b []byte) error {
	return c.raw.Write(ctx, Binary, b)
}

// ReadJSON 读取一条消息并按 JSON 反序列化到 v。
func (c *Conn) ReadJSON(ctx context.Context, v any) error {
	return wsjson.Read(ctx, c.raw, v)
}

// WriteJSON 将 v 序列化为 JSON 文本消息写出。
func (c *Conn) WriteJSON(ctx context.Context, v any) error {
	return wsjson.Write(ctx, c.raw, v)
}

// Ping 发送 ping 并等待 pong，可用于探活。
func (c *Conn) Ping(ctx context.Context) error {
	return c.raw.Ping(ctx)
}

// Subprotocol 返回握手协商出的子协议（未协商为空串）。
func (c *Conn) Subprotocol() string {
	return c.raw.Subprotocol()
}

// Close 以指定状态码主动关闭连接（发送关闭帧）。
func (c *Conn) Close(code StatusCode, reason string) error {
	return c.raw.Close(code, reason)
}

// Raw 返回底层 *websocket.Conn，用于本封装未覆盖的高级用法。
func (c *Conn) Raw() *websocket.Conn {
	return c.raw
}

type config struct {
	subprotocols       []string
	originPatterns     []string
	insecureSkipVerify bool
	readLimit          int64
	readLimitSet       bool
}

// Option 配置 Handler 的握手行为。
type Option func(*config)

// WithSubprotocols 声明服务端支持的子协议，握手时与客户端协商。
func WithSubprotocols(s ...string) Option {
	return func(c *config) { c.subprotocols = s }
}

// WithOriginPatterns 允许这些 origin 跨域连接（path.Match 模式）。
// 默认仅允许同源。
func WithOriginPatterns(patterns ...string) Option {
	return func(c *config) { c.originPatterns = patterns }
}

// WithInsecureSkipVerify 关闭 origin 校验（仅开发/可信内网使用，生产慎用）。
func WithInsecureSkipVerify() Option {
	return func(c *config) { c.insecureSkipVerify = true }
}

// WithReadLimit 设置单条消息读取上限（字节）。传 -1 表示不限制。
// 不设置时使用库默认（32KiB）。
func WithReadLimit(n int64) Option {
	return func(c *config) { c.readLimit = n; c.readLimitSet = true }
}

// Handler 把请求升级为 WebSocket 并执行 fn。
//   - fn 接收 *http.Request，可读取 query / header / 子协议 / 鉴权信息；
//     取消信号通过 r.Context() 获取。
//   - fn 返回 nil：正常关闭（StatusNormalClosure）。
//   - fn 返回 error：以 StatusInternalError 关闭。
//
// 握手失败时 Accept 已自行写出错误响应，Handler 直接返回。
//
//	mux.Handle("/ws", ws.Handler(func(r *http.Request, c *ws.Conn) error {
//	    ctx := r.Context()
//	    for {
//	        typ, data, err := c.Read(ctx)
//	        if err != nil { return err } // 客户端关闭/出错即退出
//	        if err := c.Write(ctx, typ, data); err != nil { return err }
//	    }
//	}))
func Handler(fn func(r *http.Request, c *Conn) error, opts ...Option) http.HandlerFunc {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:       cfg.subprotocols,
			OriginPatterns:     cfg.originPatterns,
			InsecureSkipVerify: cfg.insecureSkipVerify,
		})
		if err != nil {
			return // Accept 已写出错误响应
		}
		if cfg.readLimitSet {
			raw.SetReadLimit(cfg.readLimit)
		}
		defer raw.CloseNow() // 兜底：确保连接最终被关闭（已正常 Close 时为 no-op）

		c := &Conn{raw: raw}
		if err := fn(r, c); err != nil {
			_ = raw.Close(StatusInternalError, "handler error")
			return
		}
		_ = raw.Close(StatusNormalClosure, "")
	}
}
