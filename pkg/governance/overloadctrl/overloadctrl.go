// Package overloadctrl 提供反馈驱动的自适应限流。
//
// 与 pkg/ratelimit 的区别:ratelimit 是固定阈值(令牌桶/滑动窗口),阈值由调用方预设,
// 不随负载变化;本包是自适应——根据响应延迟和错误率动态收紧/放宽通过率,无需人工调参。
//
// 接口(对齐 trpc-go overloadctrl):Acquire(ctx, addr) 返回 Token,请求结束后
// Token.OnResponse(ctx, err) 反馈结果,controller 据此更新该 addr 的负载画像。
// 调用方在 client 调用前 Acquire,拿到 Token 才发请求,调用结束 OnResponse。
//
// 算法(类 BBR/CoDel 的简化版):per-addr 维护 minRTT(滑动窗口最小值,作为健康基线)
// 和 lastRTT。当 lastRTT > k*minRTT(k 默认 2.0,表示延迟翻倍)且 inFlight 超过
// minInflight(默认 10)时,认为过载,拒绝请求。错误率过高(连续错误)也收紧。
// 反馈闭环:OnResponse 更新 lastRTT/minRTT/inFlight/错误计数。
//
// 恢复:延迟梯度触发的拒绝会随在途请求自然排空(inFlight 降到 minInflight 以下)自动放开;
// 连续错误触发的锁定态则每隔 errRecovInterval(默认 5s)放行一个探测请求,探测成功解锁、
// 失败续锁——确保节点不会因连续错误被永久拒绝。
package overloadctrl

import (
	"context"
	"errors"
	"sync"
	"time"

	"log/slog"
)

// ErrOverloaded 节点过载,请求被拒绝。
var ErrOverloaded = errors.New("overload controller rejected: node overloaded")

// OverloadController 自适应限流器接口。
type OverloadController interface {
	Acquire(ctx context.Context, addr string) (Token, error)
}

// Token 请求许可,请求结束后必须调用 OnResponse 反馈结果。
type Token interface {
	OnResponse(ctx context.Context, err error)
}

// NoopController 空实现。Acquire 永远放行,OnResponse 空操作。默认值,零开销。
type NoopController struct{}

func (NoopController) Acquire(context.Context, string) (Token, error) { return NoopToken{}, nil }

// NoopToken 空 Token。
type NoopToken struct{}

func (NoopToken) OnResponse(context.Context, error) {}

// addrState 单个 addr 的负载画像。
type addrState struct {
	mu sync.Mutex
	// RTT 采样
	minRTT     time.Duration // 滑动窗口内的最小 RTT(健康基线)
	lastRTT    time.Duration // 最近一次 RTT
	rttSamples []time.Duration
	// 并发计数
	inFlight int // 当前在途请求数
	// 错误追踪
	consecutiveErrors uint32
	errLockedAt       time.Time // 连续错误达阈值、进入锁定态的时刻(用于冷却放行探测)
	errProbeInflight  bool      // 锁定态是否已放行一个探测请求
	// 配置快照(从 controller 复制,避免热路径读 controller 锁)
	minInflight      int
	rttMultiple      float64
	rttWindow        int
	errThreshold     uint32
	errRecovInterval time.Duration
}

// config 配置(不导出,通过 Option 设置)。
type config struct {
	rttMultiple      float64       // 延迟梯度阈值:lastRTT > rttMultiple*minRTT 视为过载
	minInflight      int           // inFlight 低于此值不触发(避免低负载误判)
	rttWindow        int           // minRTT 采样窗口大小
	errThreshold     uint32        // 连续错误达此值也拒绝(独立于延迟)
	errRecovInterval time.Duration // 错误锁定后每隔多久放行一个探测请求(默认 5s)
	onDrop           func(addr string)
}

// Option 配置 AdaptiveController。
type Option func(*config)

// WithRTTMultiple 设置延迟梯度阈值倍数(默认 2.0,即延迟翻倍视为过载)。
func WithRTTMultiple(k float64) Option { return func(c *config) { c.rttMultiple = k } }

// WithMinInflight 设置触发过载判断的最小在途请求数(默认 10)。
// 低于此值时即使延迟高也不拒绝(避免低负载时的偶发延迟误判)。
func WithMinInflight(n int) Option { return func(c *config) { c.minInflight = n } }

// WithRTTWindow 设置 minRTT 采样窗口大小(默认 20)。
func WithRTTWindow(n int) Option { return func(c *config) { c.rttWindow = n } }

// WithErrorThreshold 设置连续错误阈值(默认 5),达此值直接拒绝。
func WithErrorThreshold(n uint32) Option { return func(c *config) { c.errThreshold = n } }

// WithErrorRecoveryInterval 设置错误锁定后的探测放行间隔(默认 5s)。
// 连续错误达阈值进入锁定态后,每隔此间隔放行一个探测请求;探测成功则解除锁定,
// 失败则继续锁定。避免节点因连续错误被永久拒绝、无法恢复。
func WithErrorRecoveryInterval(d time.Duration) Option {
	return func(c *config) { c.errRecovInterval = d }
}

// WithOnDrop 设置请求被拒时的回调(打 metric/日志用)。
func WithOnDrop(fn func(addr string)) Option { return func(c *config) { c.onDrop = fn } }

// AdaptiveController 基于延迟梯度的自适应限流器。按 addr 维护独立负载画像。
type AdaptiveController struct {
	cfg    config
	mu     sync.RWMutex
	states map[string]*addrState
}

// NewAdaptiveController 创建自适应限流器。
func NewAdaptiveController(opts ...Option) *AdaptiveController {
	cfg := config{
		rttMultiple:      2.0,
		minInflight:      10,
		rttWindow:        20,
		errThreshold:     5,
		errRecovInterval: 5 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &AdaptiveController{cfg: cfg, states: make(map[string]*addrState)}
}

// getOrCreate 取/建 addr 的状态。
func (c *AdaptiveController) getOrCreate(addr string) *addrState {
	c.mu.RLock()
	s, ok := c.states[addr]
	c.mu.RUnlock()
	if ok {
		return s
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok = c.states[addr]; ok {
		return s
	}
	s = &addrState{
		minInflight:      c.cfg.minInflight,
		rttMultiple:      c.cfg.rttMultiple,
		rttWindow:        c.cfg.rttWindow,
		errThreshold:     c.cfg.errThreshold,
		errRecovInterval: c.cfg.errRecovInterval,
		rttSamples:       make([]time.Duration, 0, c.cfg.rttWindow),
	}
	c.states[addr] = s
	return s
}

// Acquire 判断 addr 是否可放行。过载(延迟梯度超阈 + inFlight 足够)或连续错误超阈时拒绝。
func (c *AdaptiveController) Acquire(_ context.Context, addr string) (Token, error) {
	s := c.getOrCreate(addr)
	s.mu.Lock()
	defer s.mu.Unlock()

	// 连续错误超阈,进入锁定态。每隔 errRecovInterval 放行一个探测请求,
	// 探测结果由 OnResponse 反馈(成功清零解锁,失败重置计时继续锁定),
	// 避免节点因连续错误被永久拒绝、无法恢复。
	if s.consecutiveErrors >= s.errThreshold {
		canProbe := !s.errProbeInflight &&
			s.errRecovInterval > 0 &&
			!s.errLockedAt.IsZero() &&
			time.Since(s.errLockedAt) >= s.errRecovInterval
		if !canProbe {
			c.fireOnDrop(addr)
			return nil, ErrOverloaded
		}
		// 放行一个探测请求
		s.errProbeInflight = true
		s.inFlight++
		return &adaptiveToken{controller: c, addr: addr, start: time.Now()}, nil
	}
	// 延迟梯度:有 minRTT 基线 + lastRTT 飙升 + 在途请求足够多时拒绝
	if s.minRTT > 0 && s.inFlight >= s.minInflight {
		if s.lastRTT > time.Duration(s.rttMultiple*float64(s.minRTT)) {
			c.fireOnDrop(addr)
			return nil, ErrOverloaded
		}
	}
	s.inFlight++
	return &adaptiveToken{controller: c, addr: addr, start: time.Now()}, nil
}

func (c *AdaptiveController) fireOnDrop(addr string) {
	if c.cfg.onDrop != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("overloadctrl onDrop panic", "addr", addr, "panic", r)
				}
			}()
			c.cfg.onDrop(addr)
		}()
	}
}

// adaptiveToken 记录请求开始时刻,OnResponse 时计算 RTT 并反馈。
type adaptiveToken struct {
	controller *AdaptiveController
	addr       string
	start      time.Time
	released   bool
}

// OnResponse 反馈请求结果。err==nil 视为成功,清零连续错误;否则累加。
// cost = time.Since(start),用于更新 lastRTT 和 minRTT 窗口。
func (t *adaptiveToken) OnResponse(_ context.Context, err error) {
	if t.released {
		return
	}
	t.released = true
	s := t.controller.getOrCreate(t.addr)
	rtt := time.Since(t.start)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight--
	if s.inFlight < 0 {
		s.inFlight = 0
	}

	// 更新 RTT 画像
	s.lastRTT = rtt
	s.rttSamples = append(s.rttSamples, rtt)
	if len(s.rttSamples) > s.rttWindow {
		s.rttSamples = s.rttSamples[len(s.rttSamples)-s.rttWindow:]
	}
	min := rtt
	for _, r := range s.rttSamples {
		if r < min {
			min = r
		}
	}
	s.minRTT = min

	// 错误计数与锁定态恢复
	if err != nil {
		s.consecutiveErrors++
		// 若这是锁定态放行的探测,探测失败 → 重置计时,继续锁定下一个 errRecovInterval。
		if s.errProbeInflight {
			s.errProbeInflight = false
			s.errLockedAt = time.Now()
		} else if s.consecutiveErrors >= s.errThreshold && s.errLockedAt.IsZero() {
			// 刚跨过阈值,进入锁定态,从此刻开始计冷却。
			s.errLockedAt = time.Now()
		}
	} else {
		// 成功(含锁定态探测成功)→ 清零错误计数并解除锁定。
		s.consecutiveErrors = 0
		s.errProbeInflight = false
		s.errLockedAt = time.Time{}
	}
}

// OverloadStats 单个 addr 的负载画像快照。
type OverloadStats struct {
	Addr              string        `json:"addr"`
	InFlight          int           `json:"in_flight"`
	MinRTT            time.Duration `json:"min_rtt"`
	LastRTT           time.Duration `json:"last_rtt"`
	ConsecutiveErrors uint32        `json:"consecutive_errors"`
}

// Stats 返回所有 addr 的状态快照。
func (c *AdaptiveController) Stats() map[string]OverloadStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]OverloadStats, len(c.states))
	for addr, s := range c.states {
		s.mu.Lock()
		out[addr] = OverloadStats{
			Addr:              addr,
			InFlight:          s.inFlight,
			MinRTT:            s.minRTT,
			LastRTT:           s.lastRTT,
			ConsecutiveErrors: s.consecutiveErrors,
		}
		s.mu.Unlock()
	}
	return out
}

// Reset 重置所有 addr 的状态。
func (c *AdaptiveController) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.states {
		s.mu.Lock()
		s.inFlight = 0
		s.minRTT = 0
		s.lastRTT = 0
		s.consecutiveErrors = 0
		s.errLockedAt = time.Time{}
		s.errProbeInflight = false
		s.rttSamples = s.rttSamples[:0]
		s.mu.Unlock()
	}
}

var (
	_ OverloadController = NoopController{}
	_ OverloadController = (*AdaptiveController)(nil)
	_ Token              = NoopToken{}
	_ Token              = (*adaptiveToken)(nil)
)
