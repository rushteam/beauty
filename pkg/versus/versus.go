// Package versus 提供"限时多方对抗计分"原语:N 个阵营在一段倒计时内实时累计分,
// 到点定胜负。直播 PK、团战、答题赛、拉票等"双方/多方限时比拼"场景的通用抽象。
//
// 这是一个组合型原语,复用 beauty 已有的基础件:
//   - pkg/fsm:对局状态机(pending→running→ended),非法操作(如结束后再加分)被拒;
//   - pkg/stream:分数变化 / 开始 / 结束事件 fan-out 给订阅者(推给客户端渲染);
//   - 内建单次倒计时(time.AfterFunc):到点自动结算。单对局自带定时器比共享
//     delayqueue 更自洽(无需外部生命周期);批量对局可在上层用 delayqueue 统管。
//
// 语义:
//   - New 定义阵营与时长,初始 pending;Start 进入 running 并启动倒计时;
//   - running 态 Add(side, delta) 累加分并广播 ScoreChanged;pending/ended 态 Add 报错;
//   - 倒计时到点(或手动 Finish)进入 ended:计算领先方 / 平局,触发 OnEnd 回调 +
//     广播 Ended 事件。ended 是终态,幂等(重复 Finish 无副作用)。
//
// 并发安全。零值不可用,用 New 构造。对局结束或不再使用时调用 Close 释放资源。
package versus

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/fsm"
	"github.com/rushteam/beauty/pkg/stream"
)

// 对局状态。
type state int

const (
	statePending state = iota
	stateRunning
	stateEnded
)

// 状态机事件。
type event int

const (
	evStart event = iota
	evFinish
)

// EventType 对外广播的事件类型。
type EventType int

const (
	// EventStarted 对局开始。
	EventStarted EventType = iota
	// EventScoreChanged 某方分数变化。
	EventScoreChanged
	// EventEnded 对局结束(带最终结果)。
	EventEnded
)

func (e EventType) String() string {
	switch e {
	case EventStarted:
		return "started"
	case EventScoreChanged:
		return "score_changed"
	case EventEnded:
		return "ended"
	default:
		return "unknown"
	}
}

// Event 广播给订阅者的对局事件。
type Event struct {
	Type EventType
	// Side 仅 EventScoreChanged 有意义:发生变化的阵营。
	Side string
	// Delta 仅 EventScoreChanged 有意义:本次变化量。
	Delta int64
	// Snapshot 事件发生后的对局快照。
	Snapshot Snapshot
}

// Snapshot 对局的即时快照。
type Snapshot struct {
	ID        string           // 对局 ID
	Scores    map[string]int64 // 各阵营当前分
	Leader    string           // 领先阵营;平局为 ""
	Tie       bool             // 是否平局(最高分有多方并列)
	Running   bool             // 是否进行中
	Ended     bool             // 是否已结束
	Remaining time.Duration    // 剩余时间(ended 后为 0)
}

// Result 对局最终结果(OnEnd 回调 + EventEnded 携带)。
type Result struct {
	ID     string
	Scores map[string]int64
	Winner string // 胜方;平局为 ""
	Tie    bool
}

// config 配置。
type config struct {
	duration time.Duration
	onEnd    func(Result)
	bufSize  int
}

// Option 配置 Match。
type Option func(*config)

// WithDuration 设置对局时长(Start 后倒计时,默认 5 分钟)。
func WithDuration(d time.Duration) Option { return func(c *config) { c.duration = d } }

// WithOnEnd 设置对局结束回调(到点或手动 Finish 均触发,仅触发一次)。
func WithOnEnd(fn func(Result)) Option { return func(c *config) { c.onEnd = fn } }

// WithEventBuffer 设置事件广播的每订阅者缓冲大小(默认 16)。
func WithEventBuffer(n int) Option { return func(c *config) { c.bufSize = n } }

// Match 一场限时多方对抗。零值不可用,用 New 构造。并发安全。
type Match struct {
	id    string
	cfg   config
	sides []string

	mu       sync.Mutex
	sm       *fsm.FSM[state, event]
	scores   map[string]int64
	deadline time.Time
	timer    *time.Timer

	bus *stream.Broadcaster[Event]
}

// ErrNotRunning 对局不在进行中(pending 未开始 / ended 已结束),不能加分。
var ErrNotRunning = errors.New("versus: match not running")

// ErrUnknownSide 传入的阵营不在 New 声明的阵营列表中。
var ErrUnknownSide = errors.New("versus: unknown side")

// New 创建一场对局。id 为对局标识,sides 为参与阵营(至少 1 个,通常 2 个)。
func New(id string, sides []string, opts ...Option) *Match {
	cfg := config{duration: 5 * time.Minute, bufSize: 16}
	for _, o := range opts {
		o(&cfg)
	}
	scores := make(map[string]int64, len(sides))
	for _, s := range sides {
		scores[s] = 0
	}
	sm := fsm.NewBuilder[state, event](statePending).
		Allow(statePending, evStart, stateRunning).
		Allow(stateRunning, evFinish, stateEnded).
		Build()

	return &Match{
		id:     id,
		cfg:    cfg,
		sides:  sides,
		sm:     sm,
		scores: scores,
		bus:    stream.New[Event](stream.WithBufferSize(cfg.bufSize)),
	}
}

// Start 开始对局:进入 running 并启动倒计时。重复 Start 报错(非法转移)。
func (m *Match) Start() error {
	m.mu.Lock()
	if _, err := m.sm.Fire(evStart); err != nil {
		m.mu.Unlock()
		return err
	}
	m.deadline = time.Now().Add(m.cfg.duration)
	m.timer = time.AfterFunc(m.cfg.duration, func() { m.finish() })
	snap := m.snapshotLocked()
	m.mu.Unlock()

	m.bus.Publish(Event{Type: EventStarted, Snapshot: snap})
	return nil
}

// Add 给 side 累加 delta(可为负,如撤回礼物),返回该 side 的新分数。
// 仅 running 态可加;非 running 返回 ErrNotRunning;未知 side 返回 ErrUnknownSide。
func (m *Match) Add(side string, delta int64) (int64, error) {
	m.mu.Lock()
	if _, ok := m.scores[side]; !ok {
		m.mu.Unlock()
		return 0, ErrUnknownSide
	}
	if !m.sm.Is(stateRunning) {
		m.mu.Unlock()
		return 0, ErrNotRunning
	}
	m.scores[side] += delta
	newScore := m.scores[side]
	snap := m.snapshotLocked()
	m.mu.Unlock()

	m.bus.Publish(Event{Type: EventScoreChanged, Side: side, Delta: delta, Snapshot: snap})
	return newScore, nil
}

// Finish 手动提前结束对局(到点会自动结束,无需手动调用)。幂等。
func (m *Match) Finish() { m.finish() }

// finish 结算对局:running→ended,停表,触发 OnEnd + 广播 Ended。幂等(非 running 直接返回)。
func (m *Match) finish() {
	m.mu.Lock()
	if _, err := m.sm.Fire(evFinish); err != nil {
		m.mu.Unlock() // 已结束或未开始:幂等,无副作用
		return
	}
	if m.timer != nil {
		m.timer.Stop()
	}
	res := m.resultLocked()
	snap := m.snapshotLocked()
	onEnd := m.cfg.onEnd
	m.mu.Unlock()

	if onEnd != nil {
		onEnd(res)
	}
	m.bus.Publish(Event{Type: EventEnded, Snapshot: snap})
}

// Snapshot 返回当前对局快照。
func (m *Match) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

// Subscribe 订阅对局事件流。返回只读 channel 与退订函数。
// ctx 取消或调用退订函数即停止接收。
func (m *Match) Subscribe(ctx context.Context) (<-chan Event, func()) {
	return m.bus.Subscribe(ctx)
}

// Close 释放对局资源(停表、关闭事件广播)。幂等。
func (m *Match) Close() {
	m.mu.Lock()
	if m.timer != nil {
		m.timer.Stop()
	}
	m.mu.Unlock()
	m.bus.Close()
}

// snapshotLocked 构造快照。调用方持锁。
func (m *Match) snapshotLocked() Snapshot {
	scores := make(map[string]int64, len(m.scores))
	maps.Copy(scores, m.scores)
	leader, tie := m.leaderLocked()
	var remaining time.Duration
	if m.sm.Is(stateRunning) {
		remaining = max(time.Until(m.deadline), 0)
	}
	return Snapshot{
		ID:        m.id,
		Scores:    scores,
		Leader:    leader,
		Tie:       tie,
		Running:   m.sm.Is(stateRunning),
		Ended:     m.sm.Is(stateEnded),
		Remaining: remaining,
	}
}

// resultLocked 计算最终结果。调用方持锁。
func (m *Match) resultLocked() Result {
	winner, tie := m.leaderLocked()
	scores := make(map[string]int64, len(m.scores))
	maps.Copy(scores, m.scores)
	if tie {
		winner = ""
	}
	return Result{ID: m.id, Scores: scores, Winner: winner, Tie: tie}
}

// leaderLocked 返回当前最高分阵营;若最高分有多方并列则 tie=true。调用方持锁。
// 按 sides 声明顺序遍历以保证确定性。
func (m *Match) leaderLocked() (leader string, tie bool) {
	var best int64
	var bestCount int
	first := true
	for _, s := range m.sides {
		v := m.scores[s]
		if first || v > best {
			best = v
			leader = s
			bestCount = 1
			first = false
		} else if v == best {
			bestCount++
		}
	}
	return leader, bestCount > 1
}
