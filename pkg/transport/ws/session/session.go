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
//
// 其中 myHandler 实现 session.Handler,在 OnMessage 里读消息、用 Send 投递写。
package session

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/transport/ws"
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
	scheduleCh chan func()    // Schedule 回调队列,由读循环消费
	kickCh     chan kickCmd   // Kick 命令通道,写循环先写消息再关闭
	closeOnce  sync.Once
	stopped    atomic.Bool
	attach     sync.Map // Attachment: 会话级 KV 状态仓
	kickReason string   // Kick 原因(供 OnClose 区分)
}

type kickCmd struct {
	env    *Envelope
	reason string
}

type config struct {
	pingPeriod     time.Duration // 主动 ping 周期,<=0 不 ping
	pingTimeout    time.Duration // 单次 ping 的超时
	writeTimeout   time.Duration // 每条写的超时
	maxMessageSize int64         // 单条消息读取上限,<=0 不限
	sendQueue      int           // outgoingCh 容量
	scheduleQueue  int           // scheduleCh 容量
	pingBackoff    int           // 每收 N 条消息才 ping 一次(背压,省带宽)
	interceptors   []Interceptor // 消息拦截器链
}

// Interceptor 是 WebSocket 消息拦截器,在 OnMessage 之前执行。
// 返回:
//   - consume=true: 消息已被拦截器消费,不再传给后续拦截器和 OnMessage;
//   - err!=nil: 会话将被关闭(如认证失败、违规踢人);
//   - 两者都为默认值: 继续传给下一个拦截器或 OnMessage。
//
// 典型用途:认证检查、限流、ACL、RequestTracker 响应匹配。
type Interceptor func(s *Session, typ MessageType, data []byte) (consume bool, err error)

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

// WithScheduleQueue 设置 Schedule 回调队列容量,默认 64。
// 队列满时 Schedule 返回 false(丢弃回调),不阻塞调用方。
func WithScheduleQueue(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.scheduleQueue = n
		}
	}
}

// WithInterceptor 添加消息拦截器。多次调用按顺序组成拦截器链。
// 拦截器在 OnMessage 之前、读循环 goroutine 内串行执行,可安全访问 Session 状态。
//
// 用法:
//
//	session.Accept(handler,
//	    session.WithInterceptor(authInterceptor),     // 先认证
//	    session.WithInterceptor(rateLimitInterceptor), // 再限流
//	    session.WithInterceptor(tracker.Interceptor(extractID)), // 响应匹配
//	)
func WithInterceptor(fn Interceptor) Option {
	return func(c *config) { c.interceptors = append(c.interceptors, fn) }
}

var nextSessionID atomic.Uint64

// Accept 返回一个 ws.Handler,把每条连接升级为有状态会话并交给 h 处理。
//
//	mux.Handle("/ws", ws.Handler(session.Accept(myHandler,
//	    session.WithPingPeriod(30*time.Second),
//	), ws.WithSubprotocols("v1")))
func Accept(h Handler, opts ...Option) func(*http.Request, *ws.Conn) error {
	cfg := config{
		pingPeriod:    54 * time.Second,
		pingTimeout:   5 * time.Second,
		writeTimeout:  10 * time.Second,
		sendQueue:     256,
		scheduleQueue: 64,
		pingBackoff:   20,
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
			scheduleCh: make(chan func(), cfg.scheduleQueue),
			kickCh:     make(chan kickCmd, 1),
			id:         nextSessionID.Add(1),
		}
		return s.consume()
	}
}

// ID 返回会话的唯一自增 ID(进程内唯一)。
func (s *Session) ID() uint64 { return s.id }

// Context 返回会话生命周期 context,在会话关闭时取消。
func (s *Session) Context() context.Context { return s.ctx }

// ---- Attachment: 会话级类型安全 KV 状态仓 ----

// Set 在会话上存储一个 key-value 对。并发安全。
// 适合存放玩家信息、认证结果、自定义上下文等与会话生命周期绑定的状态。
func (s *Session) Set(key string, value any) { s.attach.Store(key, value) }

// Get 从会话取一个值。不存在或类型不匹配返回零值, false。并发安全。
func Get[T any](s *Session, key string) (T, bool) {
	v, ok := s.attach.Load(key)
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// MustGet 同 Get,不存在时 panic。适合确定已 Set 的场景。
func MustGet[T any](s *Session, key string) T {
	v, ok := Get[T](s, key)
	if !ok {
		panic("session: attachment key not found or wrong type: " + key)
	}
	return v
}

// Delete 删除会话上的一个 key。
func (s *Session) Delete(key string) { s.attach.Delete(key) }

// Range 遍历会话上的所有 key-value 对。fn 返回 false 停止遍历。
func (s *Session) Range(fn func(key string, value any) bool) {
	s.attach.Range(func(k, v any) bool {
		return fn(k.(string), v)
	})
}

// ---- Send / Schedule / Kick ----

// Send 投递一条消息到写循环队列,异步发送。会话已停止或队列满时返回 false。
// 队列满(慢客户端)会触发关闭会话,避免内存堆积。
func (s *Session) Send(typ MessageType, data []byte) bool {
	if s.stopped.Load() {
		return false
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	select {
	case s.outgoingCh <- &Envelope{Type: typ, Data: cp}:
		return true
	default:
		s.shutdown("send queue full")
		return false
	}
}

// Schedule 把回调投递到读循环 goroutine 串行执行。
// 适合从任意 goroutine 安全操作会话绑定的业务状态(无需加锁):回调在读循环
// goroutine 里与 OnMessage 串行,保证同一时刻只有一段业务代码在操作会话。
//
// 典型场景:定时器回调、广播处理、跨 goroutine 通知。
// 队列满或会话已停止时返回 false(回调丢弃),不阻塞调用方。
func (s *Session) Schedule(fn func()) bool {
	if s.stopped.Load() || fn == nil {
		return false
	}
	select {
	case s.scheduleCh <- fn:
		return true
	default:
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

// KickMessage 是 Kick 时发给客户端的结构化消息。
// 客户端可据此区分"被服务端主动踢出"与"网络断开"。
type KickMessage struct {
	Type   string `json:"type"`             // 固定 "kick"
	Code   int    `json:"code"`             // 业务踢人码(如 1=重复登录, 2=封号, 3=运维踢人)
	Reason string `json:"reason,omitempty"` // 可读原因
}

// Kick 主动踢出会话:先发一条结构化 KickMessage 让客户端知道被踢原因,再关闭连接。
// 与 Close 的区别:Kick 发可解析的消息帧,客户端能区分"被踢"vs"网络断开";
// Close 只发 WebSocket close 帧,客户端难以获取原因详情。
// Kick 通过写循环保证消息先发出再关闭(不会因 ctx 取消导致消息丢失)。
func (s *Session) Kick(code int, reason string) {
	if s.stopped.Load() {
		return
	}
	msg := KickMessage{Type: "kick", Code: code, Reason: reason}
	data, err := json.Marshal(msg)
	if err != nil {
		s.shutdown("kicked: " + reason)
		return
	}
	env := &Envelope{Type: ws.Text, Data: data}
	select {
	case s.kickCh <- kickCmd{env: env, reason: reason}:
	default:
		s.shutdown("kicked: " + reason)
	}
}

// KickReason 返回 Kick 原因。非 Kick 关闭返回空字符串。
// 可在 OnClose 中调用,判断是主动踢出还是正常断开。
func (s *Session) KickReason() string { return s.kickReason }

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

// readResult 是读 goroutine → 主循环的一条结果。
type readResult struct {
	typ  MessageType
	data []byte
	err  error
}

func (s *Session) readLoop() string {
	// 读操作在独立 goroutine 完成,结果通过 channel 投递到主循环;
	// 主循环 select readCh + scheduleCh,保证 OnMessage 与 Schedule 串行。
	readCh := make(chan readResult, 1)
	go func() {
		for {
			typ, data, err := s.conn.Read(s.readCtx)
			readCh <- readResult{typ: typ, data: data, err: err}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case r := <-readCh:
			if r.err != nil {
				s.drainSchedule()
				return r.err.Error()
			}
			// 拦截器链:在 OnMessage 之前执行
			consumed := false
			for _, fn := range s.cfg.interceptors {
				consume, err := fn(s, r.typ, r.data)
				if err != nil {
					s.drainSchedule()
					return "interceptor error: " + err.Error()
				}
				if consume {
					consumed = true
					break
				}
			}
			if !consumed {
				if err := s.handler.OnMessage(s, r.typ, r.data); err != nil {
					s.drainSchedule()
					return "handler error: " + err.Error()
				}
			}
			s.drainSchedule()
		case fn := <-s.scheduleCh:
			fn()
		}
	}
}

// drainSchedule 排空 scheduleCh 中的待执行回调,在每次 OnMessage 后调用,
// 保证攒积的 Schedule 回调及时运行而非等下一条消息。
func (s *Session) drainSchedule() {
	for {
		select {
		case fn := <-s.scheduleCh:
			fn()
		default:
			return
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
		case k := <-s.kickCh:
			// Kick: 先写 kick 消息,再关闭。保证客户端收到原因。
			s.writeNow(k.env)
			s.kickReason = k.reason
			s.shutdown("kicked: " + k.reason)
			return
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
