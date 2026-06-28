// Package match 提供有状态实时会话原语,采用 actor 模型:
// 每个会话(房间/对战/协作编辑)由独立 goroutine 驱动,固定帧率 tick,
// 所有输入(业务数据、成员变更、同步信号)经带缓冲 channel 串行消费,
// 状态封装在 goroutine 内,无需锁。
//
// 单 goroutine + ticker + 背压降级模式。
// 适用场景:游戏房间、权威对战、协作编辑、实时同步状态机等需要
// "固定帧率 + 串行状态更新 + 慢消费降级"的长连接会话。
//
// 零值不可用,请用 New 构造。
package match

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Presence 表示一个会话成员的在场信息。Join/Leave 时由框架传入 Handler.Tick。
type Presence struct {
	UserID    string
	SessionID string
	Username  string
	Meta      map[string]string
}

// Handler 由业务实现,定义会话的状态转移。
// 三个类型参数:
//   - S: 会话状态(如房间快照、棋盘),由 Init 创建、Tick 演进。
//   - I: 单条输入(如玩家操作),通过 QueueInput 投递。
//   - O: 单条产出(如广播消息),Tick 返回后扇出给所有订阅者。
type Handler[S any, I any, O any] interface {
	// Init 初始化会话状态并返回期望的帧率(Hz,每秒 tick 次数)。
	// rate<=0 时回退到 WithTickRate 设定值。params 由 New 透传。
	Init(params map[string]any) (state S, rate int, err error)

	// Tick 在每个帧周期被调用,接收本帧累积的全部输入与成员变更,
	// 返回演进后的新状态与待广播的产出。返回 error 会终止会话。
	Tick(ctx context.Context, state S, inputs []I, join []Presence, leave []Presence) (S, []O, error)
}

// Match 是一个有状态实时会话实例。零值不可用,用 New 创建。
type Match[S any, I any, O any] struct {
	handler Handler[S, I, O]
	params  map[string]any
	cfg     config

	// 输入队列(由外部写入,loop 读取)。
	inputCh chan I
	joinCh  chan Presence
	leaveCh chan Presence

	// callCh 统一串行执行帧(tick)与同步信号(signal)。
	callCh  chan func(*Match[S, I, O])
	stopCh  chan struct{}
	stopped atomic.Bool

	// 运行时状态(仅 loop goroutine 读写)。
	state S
	rate  int
	tick  atomic.Int64
	err   atomic.Pointer[error]

	// 产出扇出。
	subsMu sync.RWMutex
	subs   map[*subscriber[O]]struct{}

	// 空闲计数。
	emptyTicks    int64
	maxEmptyTicks int64

	// 生命周期。
	startOnce sync.Once
	stopOnce  sync.Once
	done      chan struct{}
}

type subscriber[O any] struct {
	ch     chan O
	once   sync.Once
	closed chan struct{}
}

// Option 配置 Match。
type Option func(*config)

type config struct {
	tickRate      int  // Hz
	inputQueue    int  // inputCh 容量
	joinQueue     int  // joinCh 容量
	leaveQueue    int  // leaveCh 容量
	callQueue     int  // callCh(tick+signal)容量
	subBufSize    int  // 每个订阅者 channel 容量
	maxIdleSec    int  // 空闲多少秒后自动停止,0=不限制
	subDropOldest bool // 订阅者队列满时丢最旧(否则丢最新)
}

// WithTickRate 设置帧率(Hz),默认 10。必须 > 0。
func WithTickRate(hz int) Option {
	return func(c *config) {
		if hz > 0 {
			c.tickRate = hz
		}
	}
}

// WithInputQueue 设置输入队列容量,默认 128。队列满时 QueueInput 丢弃并返回 false。
func WithInputQueue(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.inputQueue = n
		}
	}
}

// WithCallQueue 设置帧与信号的串行执行队列容量,默认 64。
// 队列满时视为 loop 过载,会停止会话以避免无限堆积。
func WithCallQueue(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.callQueue = n
		}
	}
}

// WithMaxIdleSec 设置空闲自动停止的秒数:连续 N 帧(=rate*sec)既无输入也无订阅者即停止。
// 默认 0 表示不自动停止。
func WithMaxIdleSec(sec int) Option {
	return func(c *config) { c.maxIdleSec = sec }
}

// WithSubBufferSize 设置每个订阅者 channel 容量,默认 64。
func WithSubBufferSize(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.subBufSize = n
		}
	}
}

// WithSubDropOldest 设置订阅者队列满时丢弃策略:true 丢最旧(默认),false 丢最新。
func WithSubDropOldest(b bool) Option {
	return func(c *config) { c.subDropOldest = b }
}

// New 创建一个会话实例。handler 由业务实现,params 透传给 Handler.Init。
//
//	m := match.New[State, Input, Msg](roomHandler{}, nil,
//	    match.WithTickRate(20),
//	    match.WithMaxIdleSec(30),
//	)
//	m.Start(ctx)
func New[S any, I any, O any](handler Handler[S, I, O], params map[string]any, opts ...Option) *Match[S, I, O] {
	cfg := config{
		tickRate:      10,
		inputQueue:    128,
		joinQueue:     64,
		leaveQueue:    64,
		callQueue:     64,
		subBufSize:    64,
		maxIdleSec:    0,
		subDropOldest: true,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Match[S, I, O]{
		handler: handler,
		params:  params,
		cfg:     cfg,
		inputCh: make(chan I, cfg.inputQueue),
		joinCh:  make(chan Presence, cfg.joinQueue),
		leaveCh: make(chan Presence, cfg.leaveQueue),
		callCh:  make(chan func(*Match[S, I, O]), cfg.callQueue),
		stopCh:  make(chan struct{}),
		subs:    make(map[*subscriber[O]]struct{}),
		done:    make(chan struct{}),
	}
}

// Start 启动会话 loop goroutine。多次调用幂等,仅首次生效。
// 返回 Init 的错误(若有)。ctx 取消时触发优雅停止。
func (m *Match[S, I, O]) Start(ctx context.Context) error {
	var initErr error
	m.startOnce.Do(func() {
		state, rate, err := m.handler.Init(m.params)
		if err != nil {
			initErr = err
			m.stopped.Store(true)
			close(m.done)
			return
		}
		if rate <= 0 {
			rate = m.cfg.tickRate
		}
		m.state = state
		m.rate = rate
		m.maxEmptyTicks = int64(rate) * int64(m.cfg.maxIdleSec)
		go m.loop(ctx)
	})
	return initErr
}

// QueueInput 非阻塞投递一条输入。会话已停止或队列满时返回 false(丢弃)。
// 丢弃而非阻塞:慢消费不应拖垮生产端,背压策略。
// 输入在 inputCh 中累积,由下一次 tick 批量 drain 并交给 Handler.Tick。
func (m *Match[S, I, O]) QueueInput(in I) bool {
	if m.stopped.Load() {
		return false
	}
	select {
	case m.inputCh <- in:
		return true
	default:
		return false // 队列满,丢弃
	}
}

// QueueJoin 非阻塞投递一个成员加入事件。队列满时丢弃。
func (m *Match[S, I, O]) QueueJoin(p Presence) bool {
	if m.stopped.Load() {
		return false
	}
	select {
	case m.joinCh <- p:
		return true
	default:
		return false
	}
}

// QueueLeave 非阻塞投递一个成员离开事件。队列满时丢弃。
func (m *Match[S, I, O]) QueueLeave(p Presence) bool {
	if m.stopped.Load() {
		return false
	}
	select {
	case m.leaveCh <- p:
		return true
	default:
		return false
	}
}

// Signal 投递一个同步调用,在 loop goroutine 内串行执行,可安全读取 state。
// fn 收到的 S 即当前会话状态(只读快照)。会话已停止或过载时返回 false。
// signal 与 tick 共享 callCh,队列满时视为过载,会停止会话。
func (m *Match[S, I, O]) Signal(fn func(S)) bool {
	if m.stopped.Load() {
		return false
	}
	return m.queueCall(func(_ *Match[S, I, O]) {
		fn(m.state)
	})
}

// Subscribe 注册一个订阅者,返回只读 channel 与取消函数。
// Tick 返回的产出会扇出给所有订阅者;某订阅者队列满时按策略丢弃(默认丢最旧)。
// ctx 取消或 Match.Stop 时自动注销并关闭 channel。
func (m *Match[S, I, O]) Subscribe(ctx context.Context) (<-chan O, func()) {
	if m.stopped.Load() {
		ch := make(chan O)
		close(ch)
		return ch, func() {}
	}
	s := &subscriber[O]{
		ch:     make(chan O, m.cfg.subBufSize),
		closed: make(chan struct{}),
	}
	m.subsMu.Lock()
	m.subs[s] = struct{}{}
	m.subsMu.Unlock()

	cancel := func() { m.unsubscribe(s) }
	if ctx != nil && ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-s.closed:
			}
		}()
	}
	return s.ch, cancel
}

func (m *Match[S, I, O]) unsubscribe(s *subscriber[O]) {
	m.subsMu.Lock()
	if _, ok := m.subs[s]; ok {
		delete(m.subs, s)
		s.once.Do(func() {
			close(s.closed)
			close(s.ch)
		})
	}
	m.subsMu.Unlock()
}

// Stop 优雅停止会话:关闭订阅者,通知 loop 退出。幂等。
// 用 Wait() 等待 loop 完全退出。
func (m *Match[S, I, O]) Stop() {
	m.stopOnce.Do(func() {
		m.stopped.Store(true)
		close(m.stopCh)
	})
}

// Wait 阻塞直到 loop goroutine 退出。Init 失败或 Tick 出错时返回该错误。
func (m *Match[S, I, O]) Wait() error {
	<-m.done
	if p := m.err.Load(); p != nil {
		return *p
	}
	return nil
}

// Stopped 返回会话是否已停止。
func (m *Match[S, I, O]) Stopped() bool { return m.stopped.Load() }

// TickCount 返回已执行的 tick 数(近似)。
func (m *Match[S, I, O]) TickCount() int64 { return m.tick.Load() }

// Rate 返回会话帧率(Hz)。
func (m *Match[S, I, O]) Rate() int { return m.rate }

// loop 是会话主循环:单 goroutine 串行消费所有输入,固定帧率驱动 Tick。
// 输入/成员变更在带缓冲 channel 中累积,每次 tick 触发时批量 drain。
func (m *Match[S, I, O]) loop(ctx context.Context) {
	defer close(m.done)
	defer m.closeSubs()

	ticker := time.NewTicker(time.Second / time.Duration(m.rate))
	defer ticker.Stop()

	// ctx 取消时触发 Stop。
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				m.Stop()
			case <-m.stopCh:
			}
		}()
	}

	// 帧回调:排空累积输入并执行一次 Handler.Tick。
	tickFn := func(_ *Match[S, I, O]) {
		m.drainAndTick(ctx)
	}

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if !m.queueCall(tickFn) {
				return // 过载,已 Stop
			}
		case call := <-m.callCh:
			call(m)
		}
	}
}

// drainAndTick 排空所有累积输入与成员变更,执行一次 Handler.Tick,扇出产出。
// 仅在 loop goroutine 内通过 callCh 调用,故对 m.state 的读写无锁安全。
func (m *Match[S, I, O]) drainAndTick(ctx context.Context) {
	inputs := drain(m.inputCh)
	joins := drain(m.joinCh)
	leaves := drain(m.leaveCh)

	state, outs, err := m.handler.Tick(ctx, m.state, inputs, joins, leaves)
	if err != nil {
		e := err
		m.err.Store(&e)
		m.Stop()
		return
	}
	m.state = state
	m.tick.Add(1)

	for _, o := range outs {
		m.fanout(o)
	}

	// 空闲检测:无输入、无产出且无订阅者。
	if len(inputs) == 0 && len(outs) == 0 && m.subscriberCount() == 0 {
		m.emptyTicks++
		if m.maxEmptyTicks > 0 && m.emptyTicks >= m.maxEmptyTicks {
			m.Stop()
			return
		}
	} else {
		m.emptyTicks = 0
	}
}

// queueCall 把一个调用排入 callCh 串行执行。
// 队列满(过载)时停止会话,避免滞后无限堆积。
func (m *Match[S, I, O]) queueCall(f func(*Match[S, I, O])) bool {
	if m.stopped.Load() {
		return false
	}
	select {
	case m.callCh <- f:
		return true
	default:
		e := fmt.Errorf("match: call queue full, loop overloaded, stopping")
		m.err.Store(&e)
		m.Stop()
		return false
	}
}

func (m *Match[S, I, O]) fanout(o O) {
	m.subsMu.RLock()
	defer m.subsMu.RUnlock()
	for s := range m.subs {
		m.send(s, o)
	}
}

func (m *Match[S, I, O]) send(s *subscriber[O], o O) {
	select {
	case s.ch <- o:
		return
	default:
	}
	if !m.cfg.subDropOldest {
		return // DropNewest:已丢
	}
	// DropOldest:丢一条最旧的再试。
	select {
	case <-s.ch:
	default:
	}
	select {
	case s.ch <- o:
	default:
	}
}

func (m *Match[S, I, O]) subscriberCount() int {
	m.subsMu.RLock()
	defer m.subsMu.RUnlock()
	return len(m.subs)
}

func (m *Match[S, I, O]) closeSubs() {
	m.subsMu.Lock()
	for s := range m.subs {
		s.once.Do(func() {
			close(s.closed)
			close(s.ch)
		})
		delete(m.subs, s)
	}
	m.subsMu.Unlock()
}

// drain 非阻塞排空 channel 中当前累积的全部值。
func drain[T any](ch <-chan T) []T {
	var out []T
	for {
		select {
		case v := <-ch:
			out = append(out, v)
		default:
			return out
		}
	}
}
