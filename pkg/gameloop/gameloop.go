// Package gameloop 是一个「机制而非策略」的定步长游戏循环原语,用于在 beauty 上
// 搭帧同步(lockstep)/状态同步的服务端骨架。
//
// 它只负责四件苦活:
//   - 定步长 tick:按固定频率驱动逻辑帧(lockstep 的命脉是「所有端帧率一致」);
//   - 输入聚合:并发收集各玩家输入,每帧原子地取走「上一帧以来的全部输入」;
//   - 扇出下发:把 OnTick 产出的东西经 stream.Broadcaster 推给所有订阅连接;
//   - 生命周期:结构上满足 beauty.Service(Start/String)+ ReadyNotifier,可直接
//     beauty.WithService(room) 挂进框架,随 app 优雅停机。
//
// 它刻意「不懂」任何同步策略——帧同步 vs 状态同步、确定性、序列化、快照/增量、
// AOI,全在你的 Handler.OnTick 里决定。这条边界正是它保持轻量、不膨胀成「同步
// 引擎」的原因(本包仅依赖 pkg/stream)。
//
// 连接层不在本包职责内:调用方用 pkg/ws 把连接的「收到消息→Push」「订阅→写回」
// 接上即可(见 examples/gameloop 的 lockstep demo)。
package gameloop

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/stream"
)

// PlayerInput 是某个玩家在某一帧提交的一条输入。
type PlayerInput[In any] struct {
	Player string `json:"player"`
	Input  In     `json:"input"`
}

// Handler 定义每帧要做什么。OnTick 在 tick goroutine 内被串行调用:
// frame 是自增帧号(从 1 起),inputs 是「上一帧以来收集到的全部玩家输入」
// (可能为空)。返回的每个 Out 都会被 Publish 给所有订阅者。
//
//   - 帧同步:原样把 inputs 打包成一帧广播(客户端拿到全体输入后确定性重放);
//   - 状态同步:在这里跑服务器权威模拟,产出快照/增量(可配合 pkg/spatial 做 AOI)。
type Handler[In any, Out any] interface {
	OnTick(frame uint64, inputs []PlayerInput[In]) []Out
}

// HandlerFunc 把普通函数适配成 Handler。
type HandlerFunc[In any, Out any] func(frame uint64, inputs []PlayerInput[In]) []Out

// OnTick 实现 Handler。
func (f HandlerFunc[In, Out]) OnTick(frame uint64, inputs []PlayerInput[In]) []Out {
	return f(frame, inputs)
}

type config struct {
	name    string
	bufSize int
}

// Option 配置 Room。
type Option func(*config)

// WithName 设置房间名(仅用于 String()/日志)。
func WithName(name string) Option {
	return func(c *config) {
		if name != "" {
			c.name = name
		}
	}
}

// WithBufferSize 设置每个订阅连接的下发队列容量(默认 64)。队列写满时按
// stream.Broadcaster 的默认策略丢最旧——慢客户端不拖垮 tick 循环与其他连接。
func WithBufferSize(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.bufSize = n
		}
	}
}

// Room 是一个定步长游戏循环(一个「房间」)。零值不可用,用 New 构造。并发安全。
type Room[In any, Out any] struct {
	name    string
	rate    time.Duration
	handler Handler[In, Out]
	bc      *stream.Broadcaster[Out]

	mu      sync.Mutex
	pending []PlayerInput[In]

	frame     atomic.Uint64
	ready     chan struct{}
	readyOnce sync.Once
}

// New 创建一个房间。rate 是逻辑帧间隔(如 50ms ≈ 20Hz);rate<=0 时取 50ms。
// handler 定义每帧行为。
func New[In any, Out any](rate time.Duration, handler Handler[In, Out], opts ...Option) *Room[In, Out] {
	cfg := config{name: "room", bufSize: 64}
	for _, o := range opts {
		o(&cfg)
	}
	if rate <= 0 {
		rate = 50 * time.Millisecond
	}
	return &Room[In, Out]{
		name:    cfg.name,
		rate:    rate,
		handler: handler,
		bc:      stream.New[Out](stream.WithBufferSize(cfg.bufSize)),
		ready:   make(chan struct{}),
	}
}

// Push 提交一条玩家输入(线程安全)。它会在下一个 tick 被 OnTick 收到。
// 连接的读循环里,每收到一条客户端消息就 Push 一次。
func (r *Room[In, Out]) Push(player string, in In) {
	r.mu.Lock()
	r.pending = append(r.pending, PlayerInput[In]{Player: player, Input: in})
	r.mu.Unlock()
}

// drain 原子取走并清空本帧累积的输入。
func (r *Room[In, Out]) drain() []PlayerInput[In] {
	r.mu.Lock()
	in := r.pending
	r.pending = nil
	r.mu.Unlock()
	return in
}

// Subscribe 订阅本房间的下发流。连接的写循环 range 这个 channel 把每个 Out 写回
// 客户端;ctx 取消(连接断开)即自动退订。
func (r *Room[In, Out]) Subscribe(ctx context.Context) (<-chan Out, func()) {
	return r.bc.Subscribe(ctx)
}

// Frame 返回当前帧号(已推进的 tick 数)。
func (r *Room[In, Out]) Frame() uint64 { return r.frame.Load() }

// Start 按固定步长驱动房间,直到 ctx 取消——满足 beauty.Service。
func (r *Room[In, Out]) Start(ctx context.Context) error {
	r.readyOnce.Do(func() { close(r.ready) })
	t := time.NewTicker(r.rate)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.bc.Close() // 关闭所有订阅,写循环随之退出
			return nil
		case <-t.C:
			f := r.frame.Add(1)
			for _, out := range r.handler.OnTick(f, r.drain()) {
				r.bc.Publish(out)
			}
		}
	}
}

// Ready 在 tick 循环启动后关闭——满足 beauty.ReadyNotifier。
func (r *Room[In, Out]) Ready() <-chan struct{} { return r.ready }

// String 满足 beauty.Service。
func (r *Room[In, Out]) String() string { return "gameloop.Room(" + r.name + ")" }
