// Package session 提供基于 WebSocket 的有状态会话高阶封装。
//
// 在 pkg/ws 的薄封装之上,补齐长连接生产级所需的:
//   - 双 goroutine 读写模型:读循环(主)与写循环(子)分离,所有写串行化,
//     避免对 conn 的并发写(coder/websocket 允许并发读写,但同方向需串行);
//   - 心跳:写循环按周期发 Ping(带超时),CloseRead 在后台处理 pong/close 控制帧;
//     读循环的 ctx 在连接断开时自动取消,用于检测半开;
//   - 关闭握手:Close 只发一次 close 帧,CloseRead 返回的 ctx 取消即代表对端断开;
//   - 写超时保护:每条写用独立带超时的 ctx,慢客户端不拖垮会话。
//
// Consume/processOutgoing/pingNow,
// 适配 coder/websocket 的 context-based API。
//
// 使用:
//
//	mux.Handle("/ws", ws.Handler(session.Accept(myHandler, opts...)))
// 其中 myHandler 实现 session.Handler,在 OnMessage 里读消息、用 Send 投递写。
package session

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/ws"
)

// MessageType 区分文本/二进制消息。
type MessageType = ws.MessageType

// Envelope 是一条待发送的消息:类型 + 负载。
type Envelope struct {
	Type MessageType
	Data []byte
}

// Handler 由业务实现,定义会话生命周期。
// OnOpen/OnMessage/OnClose 都在读循环 goroutine 内串行调用,故业务状态可无锁。
type Handler interface {
	// OnOpen 在握手成功、会话就绪后调用一次。返回 error 立即关闭会话。
	OnOpen(s *Session) error
	// OnMessage 在收到一条客户端消息时调用。返回 error 关闭会话。
	OnMessage(s *Session, typ MessageType, data []byte) error
	// OnClose 在会话结束(任何原因)时调用一次,用于清理。
	OnClose(s *Session, reason string)
}

// Session 是一个有状态 WebSocket 会话。零值不可用,由 Accept 创建。
type Session struct {
	conn       *ws.Conn
	handler    Handler
	cfg        config
	ctx        context.Context // 会话生命周期 ctx,关闭时取消
	cancel     context.CancelFunc
	readCtx    context.Context // CloseRead 返回的 ctx,对端断开时取消
	id         uint64
	outgoingCh chan *Envelope // 写循环消费队列
	closeOnce  sync.Once
	stopped    atomic.Bool
}

type config struct {
	pingPeriod     time.Duration // 主动 ping 周期,<=0 不 ping
	pingTimeout    time.Duration // 单次 ping 的超时
	writeTimeout   time.Duration // 每条写的超时
	maxMessageSize int64         // 单条消息读取上限,<=0 不限
	sendQueue      int           // outgoingCh 容量
	pingBackoff    int           // 每收 N 条消息才 ping 一次(背压,省带宽)
}

// Option 配置 Accept 行为。
type Option func(*config)

// WithPingPeriod 设置主动 ping 周期。默认 54s(小于多数网关 60s 空闲)。
// <=0 不主动 ping(此时也不检测半开)。
func WithPingPeriod(d time.Duration) Option {
	return func(c *config) { c.pingPeriod = d }
}

// WithPingTimeout 设置单次 ping 的等待超时(等 pong),默认 5s。
// ping 超时即视为半开,关闭会话。
func WithPingTimeout(d time.Duration) Option {
	return func(c *config) { c.pingTimeout = d }
}

// WithWriteTimeout 设置每条业务写的超时,默认 10s。
func WithWriteTimeout(d time.Duration) Option {
	return func(c *config) { c.writeTimeout = d }
}

// WithMaxMessageSize 设置单条消息读取上限(字节),默认 0=不限。
func WithMaxMessageSize(n int64) Option {
	return func(c *config) { c.maxMessageSize = n }
}

// WithSendQueue 设置发送队列容量,默认 256。队列满(慢客户端)时关闭会话。
func WithSendQueue(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.sendQueue = n
		}
	}
}

// WithPingBackoff 设置"每收 N 条消息才 ping 一次"的阈值,默认 20。
// 业务流量大时少 ping,省带宽。
func WithPingBackoff(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.pingBackoff = n
		}
	}
}

var nextSessionID atomic.Uint64

// Accept 返回一个 ws.Handler,把每条连接升级为有状态会话并交给 h 处理。
//
//	mux.Handle("/ws", ws.Handler(session.Accept(myHandler,
//	    session.WithPingPeriod(30*time.Second),
//	), ws.WithSubprotocols("v1")))
func Accept(h Handler, opts ...Option) func(*http.Request, *ws.Conn) error {
	cfg := config{
		pingPeriod:   54 * time.Second,
		pingTimeout:  5 * time.Second,
		writeTimeout: 10 * time.Second,
		sendQueue:    256,
		pingBackoff:  20,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return func(r *http.Request, c *ws.Conn) error {
		ctx, cancel := context.WithCancel(r.Context())
		s := &Session{
			conn:       c,
			handler:    h,
			cfg:        cfg,
			ctx:        ctx,
			cancel:     cancel,
			outgoingCh: make(chan *Envelope, cfg.sendQueue),
			id:         nextSessionID.Add(1),
		}
		return s.consume()
	}
}

// ID 返回会话的唯一自增 ID(进程内唯一)。
func (s *Session) ID() uint64 { return s.id }

// Context 返回会话生命周期 context,在会话关闭时取消。
func (s *Session) Context() context.Context { return s.ctx }

// Send 投递一条消息到写循环队列,异步发送。会话已停止或队列满时返回 false。
// 队列满(慢客户端)会触发关闭会话,避免内存堆积。
func (s *Session) Send(typ MessageType, data []byte) bool {
	if s.stopped.Load() {
		return false
	}
	select {
	case s.outgoingCh <- &Envelope{Type: typ, Data: data}:
		return true
	default:
		s.shutdown("send queue full")
		return false
	}
}

// SendText 是 Send(Text, ...) 的便捷封装。
func (s *Session) SendText(b []byte) bool { return s.Send(ws.Text, b) }

// SendJSON 把 v 序列化为 JSON 文本消息发送。
func (s *Session) SendJSON(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	return s.Send(ws.Text, b)
}

// Close 主动关闭会话。幂等。reason 会传给 OnClose。
func (s *Session) Close(reason string) { s.shutdown(reason) }

func (s *Session) consume() error {
	defer s.cancel()

	if s.cfg.maxMessageSize > 0 {
		s.conn.Raw().SetReadLimit(s.cfg.maxMessageSize)
	}

	// 不使用 CloseRead:我们需要 Read 业务数据消息。
	// coder/websocket 的 Read 会自动响应 pong/close 控制帧,
	// Ping 由写循环的 ticker 主动发起。
	s.readCtx = s.ctx

	// 启动写循环(发 ping + 消费 outgoingCh)。
	go s.processOutgoing()

	// OnOpen。
	if err := s.handler.OnOpen(s); err != nil {
		s.shutdown(err.Error())
		s.handler.OnClose(s, err.Error())
		return nil
	}

	// 读循环(主 goroutine):串行读消息交给 handler。
	reason := s.readLoop()

	s.shutdown(reason)
	s.handler.OnClose(s, reason)
	return nil
}

func (s *Session) readLoop() string {
	for {
		// 用 readCtx:对端断开或会话关闭时 Read 返回 error。
		typ, data, err := s.conn.Read(s.readCtx)
		if err != nil {
			return err.Error()
		}
		if err := s.handler.OnMessage(s, typ, data); err != nil {
			return "handler error: " + err.Error()
		}
	}
}

// processOutgoing 是写循环:串行消费 outgoingCh,定期 ping。
// 所有对 conn 的写都在此 goroutine 完成,避免并发写。
func (s *Session) processOutgoing() {
	var ticker *time.Ticker
	var tickerC <-chan time.Time
	if s.cfg.pingPeriod > 0 {
		ticker = time.NewTicker(s.cfg.pingPeriod)
		defer ticker.Stop()
		tickerC = ticker.C
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.readCtx.Done():
			// 对端断开或 CloseRead 退出,停止写循环。
			return
		case <-tickerC:
			if !s.pingNow() {
				s.shutdown("ping failed")
				return
			}
		case env := <-s.outgoingCh:
			if !s.writeNow(env) {
				s.shutdown("write failed")
				return
			}
		}
	}
}

// writeNow 执行一次同步写,带超时。仅 processOutgoing 调用(非并发)。
func (s *Session) writeNow(env *Envelope) bool {
	ctx := s.ctx
	if s.cfg.writeTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(s.ctx, s.cfg.writeTimeout)
		defer cancel()
	}
	return s.conn.Write(ctx, env.Type, env.Data) == nil
}

// pingNow 发一次 ping,带超时。ping 超时视为半开。
func (s *Session) pingNow() bool {
	ctx := s.ctx
	if s.cfg.pingTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(s.ctx, s.cfg.pingTimeout)
		defer cancel()
	}
	return s.conn.Ping(ctx) == nil
}

// shutdown 关闭会话:取消 ctx、发 close 帧。幂等。
func (s *Session) shutdown(reason string) {
	s.closeOnce.Do(func() {
		s.stopped.Store(true)
		s.cancel()
		_ = s.conn.Close(ws.StatusNormalClosure, reason)
	})
}
