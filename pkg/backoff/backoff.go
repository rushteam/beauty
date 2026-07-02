// Package backoff 提供指数退避 + 抖动(jitter)的重试原语,纯标准库。
//
// 收敛散落各处的"delay *= 2 + jitter"手写退避(webhook / grpcclient / saga 等
// 各有一份),统一成可配置、可测试的一个件:计算第 n 次重试的等待时长,或直接
// 用 Retry 包住一个可能失败的操作。
//
// 退避序列:delay(n) = min(Base * Factor^n, Max),n 从 0 计;可叠加抖动打散
// "重试风暴"(大量客户端同时失败后同一时刻重试,压垮刚恢复的服务)。
// 抖动策略对齐 AWS「Exponential Backoff and Jitter」:
//   - JitterNone:精确指数,无抖动;
//   - JitterFull(默认):在 [0, delay] 均匀取值,打散最彻底;
//   - JitterEqual:delay/2 + [0, delay/2],保留一半确定性 + 一半随机。
//
// 并发安全:Policy 是不可变值(构造后只读),Duration 可并发调用;抖动用
// math/rand/v2(免播种、并发安全)。零值 Policy 不可用,用 New 构造。
package backoff

import (
	"context"
	"math"
	"math/rand/v2"
	"time"
)

// Jitter 抖动策略。
type Jitter int

const (
	// JitterFull 全抖动:等待 = [0, base*factor^n] 均匀随机(默认,打散最彻底)。
	JitterFull Jitter = iota
	// JitterEqual 半抖动:等待 = d/2 + [0, d/2],d = base*factor^n。
	JitterEqual
	// JitterNone 无抖动:等待 = base*factor^n(精确指数)。
	JitterNone
	// JitterProportional 比例抖动:等待 = d ± d*ratio,在 [d*(1-ratio), d*(1+ratio)]
	// 均匀取值(ratio 由 WithJitterRatio 设置,默认 0.25 即 ±25%)。
	// 适合"围绕名义退避小幅扰动"的场景(如 RPC 客户端重试)。
	JitterProportional
)

// config 配置。
type config struct {
	base        time.Duration
	max         time.Duration
	factor      float64
	jitter      Jitter
	jitterRatio float64
	maxRetr     int
}

// Option 配置 Policy。
type Option func(*config)

// WithBase 设置基础间隔(第 0 次重试的名义等待,默认 200ms)。
func WithBase(d time.Duration) Option { return func(c *config) { c.base = d } }

// WithMax 设置单次等待上限(默认 30s;<=0 表示不封顶)。
func WithMax(d time.Duration) Option { return func(c *config) { c.max = d } }

// WithFactor 设置指数倍率(默认 2.0)。
func WithFactor(f float64) Option { return func(c *config) { c.factor = f } }

// WithJitter 设置抖动策略(默认 JitterFull)。
func WithJitter(j Jitter) Option { return func(c *config) { c.jitter = j } }

// WithJitterRatio 设置 JitterProportional 的比例(0..1,默认 0.25 即 ±25%)。
// 仅在 jitter 为 JitterProportional 时生效。
func WithJitterRatio(r float64) Option { return func(c *config) { c.jitterRatio = r } }

// WithMaxRetries 设置 Retry 的最大重试次数(额外次数,0=只试一次;默认 3)。
func WithMaxRetries(n int) Option { return func(c *config) { c.maxRetr = n } }

// Policy 退避策略(不可变)。零值不可用,用 New 构造。并发安全。
type Policy struct {
	base        time.Duration
	max         time.Duration
	factor      float64
	jitter      Jitter
	jitterRatio float64
	maxRetr     int
}

// New 创建退避策略。
func New(opts ...Option) *Policy {
	cfg := config{
		base:        200 * time.Millisecond,
		max:         30 * time.Second,
		factor:      2.0,
		jitter:      JitterFull,
		jitterRatio: 0.25,
		maxRetr:     3,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.base <= 0 {
		cfg.base = 200 * time.Millisecond
	}
	if cfg.factor < 1 {
		cfg.factor = 2.0
	}
	if cfg.jitterRatio < 0 {
		cfg.jitterRatio = 0
	}
	if cfg.jitterRatio > 1 {
		cfg.jitterRatio = 1
	}
	if cfg.maxRetr < 0 {
		cfg.maxRetr = 0
	}
	return &Policy{
		base:        cfg.base,
		max:         cfg.max,
		factor:      cfg.factor,
		jitter:      cfg.jitter,
		jitterRatio: cfg.jitterRatio,
		maxRetr:     cfg.maxRetr,
	}
}

// Duration 返回第 attempt 次重试前应等待的时长(attempt 从 0 计)。
// 已应用倍率封顶与抖动。attempt<0 视为 0。
func (p *Policy) Duration(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	// base * factor^attempt,用 float 计算并防溢出。
	d := float64(p.base) * math.Pow(p.factor, float64(attempt))
	capped := p.max
	if capped > 0 && d > float64(capped) {
		d = float64(capped)
	}
	if d < 0 || math.IsInf(d, 1) {
		if capped > 0 {
			d = float64(capped)
		}
	}
	base := time.Duration(d)
	return applyJitter(base, p.jitter, p.jitterRatio)
}

func applyJitter(d time.Duration, j Jitter, ratio float64) time.Duration {
	if d <= 0 {
		return 0
	}
	switch j {
	case JitterNone:
		return d
	case JitterProportional:
		// [d*(1-ratio), d*(1+ratio)] 均匀。
		spread := int64(float64(d) * ratio) // 半宽
		if spread <= 0 {
			return d
		}
		return d - time.Duration(spread) + time.Duration(rand.Int64N(2*spread+1))
	case JitterEqual:
		half := d / 2
		return half + time.Duration(rand.Int64N(int64(half)+1))
	default: // JitterFull
		return time.Duration(rand.Int64N(int64(d) + 1))
	}
}

// MaxRetries 返回配置的最大重试次数。
func (p *Policy) MaxRetries() int { return p.maxRetr }

// Retry 执行 fn,失败则按退避重试,最多 MaxRetries 次(总调用 = MaxRetries+1)。
// fn 返回 nil 即成功返回;ctx 取消/超时立即返回 ctx.Err()。
// 返回最后一次的错误(全部失败时)。
//
// 若需"某些错误不重试"(如 4xx / context.Canceled),用 RetryIf。
func (p *Policy) Retry(ctx context.Context, fn func(ctx context.Context) error) error {
	return p.RetryIf(ctx, fn, nil)
}

// RetryIf 同 Retry,但用 retryable 判定错误是否值得重试:返回 false 则立即停止
// 并返回该错误(retryable 为 nil 时所有错误都重试)。
func (p *Policy) RetryIf(ctx context.Context, fn func(ctx context.Context) error, retryable func(error) bool) error {
	var lastErr error
	for attempt := 0; attempt <= p.maxRetr; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(p.Duration(attempt - 1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}
		if retryable != nil && !retryable(lastErr) {
			return lastErr
		}
	}
	return lastErr
}
