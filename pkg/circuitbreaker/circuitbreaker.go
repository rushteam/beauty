// Package circuitbreaker 提供熔断器原语:当下游错误率超过阈值时自动"断开"保护调用方,
// 经过冷却期后进入半开探测,探测成功则恢复、失败则继续断开。
//
// 三态:Closed(正常) → Open(熔断,快速失败) → HalfOpen(探测) → Closed / Open。
// 错误率用固定窗口(ring bucket)统计;Open 期间 Do 立即返回 ErrCircuitOpen。
//
// 与 beauty 弹性三件套互补:
//   - backoff:退避计算(延迟多久)
//   - hedge:对冲请求(备份并发)
//   - ratelimit:限速率(每秒多少)
//   - circuitbreaker:熔断(错误率过高时切断)
//
// 纯标准库、并发安全。
package circuitbreaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrCircuitOpen 表示熔断器处于 Open 状态,请求被快速失败。
var ErrCircuitOpen = errors.New("circuitbreaker: circuit open")

// State 是熔断器当前状态。
type State int

const (
	StateClosed   State = iota // 正常放行,统计错误率
	StateOpen                  // 熔断,快速失败
	StateHalfOpen              // 半开,允许少量探测
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Option 配置 Breaker。
type Option func(*config)

type config struct {
	threshold   float64       // 错误率阈值(0,1]，超过则 Open
	window      time.Duration // 统计窗口
	cooldown    time.Duration // Open → HalfOpen 冷却时间
	halfOpenMax int           // 半开期最多放过的探测请求数
	minRequests int           // 窗口内最少请求数(不足则不触发熔断)
	onChange    func(from, to State)
}

// WithThreshold 设置错误率阈值(默认 0.5 即 50%)。
func WithThreshold(rate float64) Option {
	return func(c *config) {
		if rate > 0 && rate <= 1 {
			c.threshold = rate
		}
	}
}

// WithWindow 设置统计窗口(默认 10s)。
func WithWindow(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.window = d
		}
	}
}

// WithCooldown 设置 Open 状态冷却期(默认 5s),到期后进入 HalfOpen。
func WithCooldown(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.cooldown = d
		}
	}
}

// WithHalfOpenMax 设置半开期最多放过的探测请求数(默认 3)。全部成功则关闭;任一失败重新 Open。
func WithHalfOpenMax(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.halfOpenMax = n
		}
	}
}

// WithMinRequests 设置触发熔断的最少请求数(默认 10),窗口内不够则不判定。
func WithMinRequests(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.minRequests = n
		}
	}
}

// WithOnStateChange 注册状态变化回调(用于日志/埋点)。
func WithOnStateChange(fn func(from, to State)) Option {
	return func(c *config) { c.onChange = fn }
}

// Breaker 是一个熔断器实例。并发安全。
type Breaker struct {
	cfg config

	mu       sync.Mutex
	state    State
	openedAt time.Time // Open 生效时刻

	// Closed 统计
	total   int64
	failures int64
	windowStart time.Time

	// HalfOpen 统计
	halfSucc int
	halfFail int
}

// New 创建熔断器。
func New(opts ...Option) *Breaker {
	cfg := config{
		threshold:   0.5,
		window:      10 * time.Second,
		cooldown:    5 * time.Second,
		halfOpenMax: 3,
		minRequests: 10,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Breaker{
		cfg:         cfg,
		state:       StateClosed,
		windowStart: time.Now(),
	}
}

// State 返回当前状态。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checkStateTransition()
	return b.state
}

// Do 包装一次调用:Closed 正常执行并统计;Open 快速失败;HalfOpen 执行探测。
// fn 返回 error 视为失败;返回 nil 视为成功。
func (b *Breaker) Do(fn func() error) error {
	b.mu.Lock()
	b.checkStateTransition()

	switch b.state {
	case StateOpen:
		b.mu.Unlock()
		return ErrCircuitOpen

	case StateHalfOpen:
		if b.halfSucc+b.halfFail >= b.cfg.halfOpenMax {
			b.mu.Unlock()
			return ErrCircuitOpen
		}
		b.mu.Unlock()
		err := fn()
		b.recordHalfOpen(err)
		return err

	default: // Closed
		b.mu.Unlock()
		err := fn()
		b.recordClosed(err)
		return err
	}
}

// Successes 返回当前窗口成功数(仅 Closed 态有效)。
func (b *Breaker) Successes() int64 {
	return atomic.LoadInt64(&b.total) - atomic.LoadInt64(&b.failures)
}

// Failures 返回当前窗口失败数。
func (b *Breaker) Failures() int64 {
	return atomic.LoadInt64(&b.failures)
}

func (b *Breaker) checkStateTransition() {
	switch b.state {
	case StateOpen:
		if time.Since(b.openedAt) >= b.cfg.cooldown {
			b.transition(StateHalfOpen)
			b.halfSucc = 0
			b.halfFail = 0
		}
	case StateClosed:
		if time.Since(b.windowStart) >= b.cfg.window {
			b.resetWindow()
		}
	}
}

func (b *Breaker) recordClosed(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if time.Since(b.windowStart) >= b.cfg.window {
		b.resetWindow()
	}

	b.total++
	if err != nil {
		b.failures++
	}

	if b.total >= int64(b.cfg.minRequests) {
		rate := float64(b.failures) / float64(b.total)
		if rate >= b.cfg.threshold {
			b.transition(StateOpen)
			b.openedAt = time.Now()
		}
	}
}

func (b *Breaker) recordHalfOpen(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err != nil {
		b.halfFail++
		b.transition(StateOpen)
		b.openedAt = time.Now()
		return
	}

	b.halfSucc++
	if b.halfSucc >= b.cfg.halfOpenMax {
		b.transition(StateClosed)
		b.resetWindow()
	}
}

func (b *Breaker) transition(to State) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	if b.cfg.onChange != nil {
		b.cfg.onChange(from, to)
	}
}

func (b *Breaker) resetWindow() {
	b.total = 0
	b.failures = 0
	b.windowStart = time.Now()
}
