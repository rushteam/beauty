// Package ratelimit 提供基于键的限流原语:令牌桶 + 滑动窗口两种实现,
// 外加 HTTP 中间件,可与 pkg/handler 组合(声明式 WithRatelimit)。
//
// 设计要点(per-user 限流 + 通用 token bucket):
//   - Limiter 接口:Allow(key) (allowed, retryAfter),按 key 隔离(如按 userID / IP);
//   - TokenBucket:固定速率补令牌,允许突发;无锁化设计用 sync.Map + 互斥桶;
//   - SlidingWindow:滑动窗口(精确),用时间戳 deque;
//   - HTTP middleware:超限返回 429 + Retry-After 头;
//   - 后台 gc:清理长时间无活动的 key,避免内存泄漏(参考 pkg/token 的 gc 模式)。
//
// 零值不可用,用 New 构造。Limiter 并发安全;Stop 后 gc goroutine 退出。
package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/ctxkey"
)

// Limiter 限流器接口。按 key 隔离(每个 key 独立计数)。
// 返回 (allowed, retryAfter):retryAfter=0 表示立即可重试或已放行。
type Limiter interface {
	Allow(key string) (allowed bool, retryAfter time.Duration)
}

// limiterKey 用于把 Limiter 注入 ctx(供 handler 声明式接入)。
var limiterKey = ctxkey.New[Limiter]()

// WithLimiter 把 limiter 装入 ctx。供 pkg/handler 的 WithRatelimit 使用。
func WithLimiter(ctx context.Context, l Limiter) context.Context {
	return ctxkey.With(ctx, limiterKey, l)
}

// FromContext 从 ctx 取出 Limiter(没有则 nil)。
func FromContext(ctx context.Context) Limiter {
	return ctxkey.MustGet(ctx, limiterKey)
}

// --- 令牌桶 ---

// bucket 单个 key 的令牌桶状态。
type bucket struct {
	mu       sync.Mutex
	tokens   float64       // 当前令牌数(可小数,因补速率是连续的)
	last      time.Time
}

// TokenBucket 令牌桶限流器:固定速率补令牌,允许突发(初始满桶)。
// burst 为桶容量(最大突发数),rate 为每秒补充令牌数。
type TokenBucket struct {
	burst      float64
	rate       float64 // tokens per second
	maxIdle    time.Duration
	gcInterval time.Duration
	mu         sync.Mutex
	bkts       map[string]*bucket
	stop       chan struct{}
	once       sync.Once
}

// NewTokenBucket 创建令牌桶限流器。burst<=0 或 rate<=0 视为不限(Allow 永远 true)。
// gc 每 gcInterval 清理一次超过 maxIdle 无活动的 key。
func NewTokenBucket(burst int, rate float64, opts ...Option) *TokenBucket {
	cfg := config{maxIdle: 5 * time.Minute, gcInterval: time.Minute}
	for _, o := range opts {
		o(&cfg)
	}
	tb := &TokenBucket{
		burst:      float64(burst),
		rate:       rate,
		maxIdle:    cfg.maxIdle,
		gcInterval: cfg.gcInterval,
		bkts:       make(map[string]*bucket),
		stop:       make(chan struct{}),
	}
	go tb.gc()
	return tb
}

// Option 配置限流器。
type Option func(*config)

type config struct {
	maxIdle    time.Duration
	gcInterval time.Duration
}

// WithMaxIdle 设置 key 最大空闲时长(超过则被 gc 回收)。默认 5min。
func WithMaxIdle(d time.Duration) Option { return func(c *config) { c.maxIdle = d } }

// WithGcInterval 设置 gc 扫描间隔。默认 1min。
func WithGcInterval(d time.Duration) Option { return func(c *config) { c.gcInterval = d } }

// Allow 按 key 限流。返回是否放行,以及建议的重试等待(超限时)。
func (tb *TokenBucket) Allow(key string) (bool, time.Duration) {
	if tb.burst <= 0 || tb.rate <= 0 {
		return true, 0
	}
	now := time.Now()
	tb.mu.Lock()
	b, ok := tb.bkts[key]
	if !ok {
		b = &bucket{tokens: tb.burst, last: now}
		tb.bkts[key] = b
	}
	tb.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	// 按经过时间补令牌。
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * tb.rate
	if b.tokens > tb.burst {
		b.tokens = tb.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// 计算补到 1 个令牌所需时间。
	retry := time.Duration((1 - b.tokens) / tb.rate * float64(time.Second))
	return false, retry
}

// gc 周期清理超过 maxIdle 无活动的 key。
func (tb *TokenBucket) gc() {
	ticker := time.NewTicker(tb.gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-tb.maxIdle)
			tb.mu.Lock()
			for k, b := range tb.bkts {
				b.mu.Lock()
				idle := b.last.Before(cutoff)
				b.mu.Unlock()
				if idle {
					delete(tb.bkts, k)
				}
			}
			tb.mu.Unlock()
		case <-tb.stop:
			return
		}
	}
}

// Stop 停止 gc goroutine。幂等。
func (tb *TokenBucket) Stop() {
	tb.once.Do(func() { close(tb.stop) })
}

// --- 滑动窗口 ---

// SlidingWindow 滑动窗口限流器:在 window 时长内最多 limit 次请求,精确计数。
type SlidingWindow struct {
	limit     int
	window    time.Duration
	maxIdle   time.Duration
	gcInterval time.Duration
	mu        sync.Mutex
	wins      map[string][]time.Time
	stop      chan struct{}
	once      sync.Once
}

// NewSlidingWindow 创建滑动窗口限流器。limit<=0 或 window<=0 视为不限。
func NewSlidingWindow(limit int, window time.Duration, opts ...Option) *SlidingWindow {
	cfg := config{maxIdle: 5 * time.Minute, gcInterval: time.Minute}
	for _, o := range opts {
		o(&cfg)
	}
	sw := &SlidingWindow{
		limit:      limit,
		window:     window,
		maxIdle:    cfg.maxIdle,
		gcInterval: cfg.gcInterval,
		wins:       make(map[string][]time.Time),
		stop:       make(chan struct{}),
	}
	go sw.gc()
	return sw
}

// Allow 按 key 限流。滑动窗口:清除早于 window 的旧记录后判断数量。
func (sw *SlidingWindow) Allow(key string) (bool, time.Duration) {
	if sw.limit <= 0 || sw.window <= 0 {
		return true, 0
	}
	now := time.Now()
	cutoff := now.Add(-sw.window)
	sw.mu.Lock()
	defer sw.mu.Unlock()
	hits := sw.wins[key]
	// 清掉窗口外的旧记录。
	i := 0
	for i < len(hits) && hits[i].Before(cutoff) {
		i++
	}
	hits = hits[i:]
	if len(hits) < sw.limit {
		hits = append(hits, now)
		sw.wins[key] = hits
		return true, 0
	}
	// 最旧一条到窗口外的时刻,就是下一个可用时间。
	retry := hits[0].Add(sw.window).Sub(now)
	retry = max(retry, 0)
	sw.wins[key] = hits
	return false, retry
}

// gc 周期清理全部记录落在窗口外的 key。
func (sw *SlidingWindow) gc() {
	ticker := time.NewTicker(sw.gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-sw.maxIdle)
			sw.mu.Lock()
			for k, hits := range sw.wins {
				if len(hits) == 0 || hits[len(hits)-1].Before(cutoff) {
					delete(sw.wins, k)
				}
			}
			sw.mu.Unlock()
		case <-sw.stop:
			return
		}
	}
}

// Stop 停止 gc goroutine。幂等。
func (sw *SlidingWindow) Stop() {
	sw.once.Do(func() { close(sw.stop) })
}

// --- HTTP 中间件 ---

// KeyFunc 从请求提取限流 key(如 IP / userID)。返回空串表示不限流(跳过)。
type KeyFunc func(r *http.Request) string

// ClientIP 从请求提取客户端 IP(优先 X-Forwarded-For,其次 RemoteAddr)。
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// 取第一个(最原始客户端)。
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	host := r.RemoteAddr
	for i := 0; i < len(host); i++ {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

// Middleware 返回 HTTP 限流中间件:keyFn 提取 key,limiter 限流。
// 超限返回 429 + Retry-After 头(秒级)。keyFn 返回空串则跳过限流。
func Middleware(l Limiter, keyFn KeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			ok, retry := l.Allow(key)
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MiddlewareWithLimiter 返回带 limiter 注入 ctx 的中间件:下游 handler
// 可用 FromContext 取 limiter 做更细粒度限流(如对某资源再限一次)。
func MiddlewareWithLimiter(l Limiter, keyFn KeyFunc) func(http.Handler) http.Handler {
	base := Middleware(l, keyFn)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(WithLimiter(r.Context(), l))
			base(next).ServeHTTP(w, r)
		})
	}
}

// String 便于日志/调试。
func (tb *TokenBucket) String() string {
	return fmt.Sprintf("TokenBucket(burst=%d, rate=%.2f/s)", int(tb.burst), tb.rate)
}
func (sw *SlidingWindow) String() string {
	return fmt.Sprintf("SlidingWindow(limit=%d, window=%s)", sw.limit, sw.window)
}
