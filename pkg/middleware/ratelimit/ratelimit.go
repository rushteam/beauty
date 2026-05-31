package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

var (
	// ErrRateLimitExceeded 超过限流阈值错误
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	// ErrRateLimiterNotFound 限流器未找到错误
	ErrRateLimiterNotFound = errors.New("rate limiter not found")
)

// Limiter 限流器接口
type Limiter interface {
	Allow() bool
	Wait(ctx context.Context) error
	Reserve() *rate.Reservation
	Limit() rate.Limit
	Burst() int
}

// KeyExtractor 键提取器接口，用于从请求中提取限流键
type KeyExtractor interface {
	Extract(ctx context.Context, metadata map[string]any) (string, error)
}

// Config 限流配置
type Config struct {
	Name          string
	Rate          float64
	Burst         int
	KeyExtractor  KeyExtractor
	EnableMetrics bool
	OnRateLimit   func(ctx context.Context, key string, rate float64)
	DefaultKey    string

	// IdleTTL 一个 key 多久没有请求后被回收，默认 10 分钟。
	// 设为 0 禁用 GC（适合 key 数量固定的场景）。
	IdleTTL time.Duration
	// GCInterval GC 扫描间隔，默认 5 分钟。
	GCInterval time.Duration
}

// limiterEntry 带最后访问时间的限流器条目
type limiterEntry struct {
	limiter  *rate.Limiter
	lastUsed atomic.Int64 // UnixNano，用 atomic 避免在 RLock 内写锁
}

func (e *limiterEntry) touch() {
	e.lastUsed.Store(time.Now().UnixNano())
}

func (e *limiterEntry) idleSince() time.Duration {
	return time.Duration(time.Now().UnixNano() - e.lastUsed.Load())
}

// RateLimitMiddleware 限流中间件
type RateLimitMiddleware struct {
	name          string
	keyExtractor  KeyExtractor
	defaultKey    string
	enableMetrics bool
	onRateLimit   func(ctx context.Context, key string, rate float64)

	mutex    sync.RWMutex
	limiters map[string]*limiterEntry
	rate     rate.Limit
	burst    int

	idleTTL    time.Duration // 0 = 禁用 GC
	gcInterval time.Duration

	statsMutex sync.RWMutex
	stats      Stats
}

// Stats 限流统计信息
type Stats struct {
	TotalRequests   uint64            `json:"total_requests"`
	AllowedRequests uint64            `json:"allowed_requests"`
	LimitedRequests uint64            `json:"limited_requests"`
	ActiveLimiters  int               `json:"active_limiters"`
	LimiterStats    map[string]uint64 `json:"limiter_stats"`
	LastLimitTime   time.Time         `json:"last_limit_time"`
}

const (
	defaultIdleTTL    = 10 * time.Minute
	defaultGCInterval = 5 * time.Minute
)

// NewRateLimitMiddleware 创建限流中间件，并在 ctx 存活期间后台运行 GC。
func NewRateLimitMiddleware(config Config) *RateLimitMiddleware {
	if config.Name == "" {
		config.Name = "rate-limit-middleware"
	}
	if config.Rate <= 0 {
		config.Rate = 100
	}
	if config.Burst <= 0 {
		config.Burst = int(config.Rate)
	}
	if config.DefaultKey == "" {
		config.DefaultKey = "default"
	}
	if config.IdleTTL == 0 {
		config.IdleTTL = defaultIdleTTL
	}
	if config.GCInterval == 0 {
		config.GCInterval = defaultGCInterval
	}

	rl := &RateLimitMiddleware{
		name:          config.Name,
		keyExtractor:  config.KeyExtractor,
		defaultKey:    config.DefaultKey,
		enableMetrics: config.EnableMetrics,
		onRateLimit:   config.OnRateLimit,
		limiters:      make(map[string]*limiterEntry),
		rate:          rate.Limit(config.Rate),
		burst:         config.Burst,
		idleTTL:       config.IdleTTL,
		gcInterval:    config.GCInterval,
		stats:         Stats{LimiterStats: make(map[string]uint64)},
	}
	return rl
}

// StartGC 启动后台 GC，ctx 取消时停止。
// 通常在应用启动时调用一次；若 IdleTTL == 0 则为空操作。
func (rl *RateLimitMiddleware) StartGC(ctx context.Context) {
	if rl.idleTTL == 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(rl.gcInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rl.gc()
			}
		}
	}()
}

// gc 清除超过 IdleTTL 未使用的 limiter。
func (rl *RateLimitMiddleware) gc() {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	for key, entry := range rl.limiters {
		if entry.idleSince() > rl.idleTTL {
			delete(rl.limiters, key)
		}
	}
}

// Name 返回中间件名称
func (rl *RateLimitMiddleware) Name() string {
	return rl.name
}

// Allow 检查请求是否允许通过
func (rl *RateLimitMiddleware) Allow(ctx context.Context, metadata map[string]any) error {
	key := rl.extractKey(ctx, metadata)
	entry := rl.getEntry(key)
	rl.recordRequest()

	if entry.limiter.Allow() {
		rl.recordAllowed(key)
		return nil
	}

	rl.recordLimited(key)
	if rl.onRateLimit != nil {
		rl.onRateLimit(ctx, key, float64(rl.rate))
	}
	return ErrRateLimitExceeded
}

// Wait 等待直到可以处理请求
func (rl *RateLimitMiddleware) Wait(ctx context.Context, metadata map[string]any) error {
	key := rl.extractKey(ctx, metadata)
	entry := rl.getEntry(key)
	rl.recordRequest()

	if err := entry.limiter.Wait(ctx); err != nil {
		rl.recordLimited(key)
		if rl.onRateLimit != nil {
			rl.onRateLimit(ctx, key, float64(rl.rate))
		}
		return err
	}

	rl.recordAllowed(key)
	return nil
}

func (rl *RateLimitMiddleware) extractKey(ctx context.Context, metadata map[string]any) string {
	if rl.keyExtractor != nil {
		if key, err := rl.keyExtractor.Extract(ctx, metadata); err == nil {
			return key
		}
	}
	return rl.defaultKey
}

// getEntry 获取或创建 limiterEntry，并更新最后访问时间。
func (rl *RateLimitMiddleware) getEntry(key string) *limiterEntry {
	rl.mutex.RLock()
	entry, exists := rl.limiters[key]
	rl.mutex.RUnlock()

	if exists {
		entry.touch()
		return entry
	}

	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	// 双重检查
	if entry, exists = rl.limiters[key]; exists {
		entry.touch()
		return entry
	}
	entry = &limiterEntry{limiter: rate.NewLimiter(rl.rate, rl.burst)}
	entry.touch()
	rl.limiters[key] = entry
	return entry
}

func (rl *RateLimitMiddleware) recordRequest() {
	if !rl.enableMetrics {
		return
	}
	rl.statsMutex.Lock()
	rl.stats.TotalRequests++
	rl.statsMutex.Unlock()
}

func (rl *RateLimitMiddleware) recordAllowed(_ string) {
	if !rl.enableMetrics {
		return
	}
	rl.statsMutex.Lock()
	rl.stats.AllowedRequests++
	rl.statsMutex.Unlock()
}

func (rl *RateLimitMiddleware) recordLimited(key string) {
	if !rl.enableMetrics {
		return
	}
	rl.statsMutex.Lock()
	rl.stats.LimitedRequests++
	rl.stats.LimiterStats[key]++
	rl.stats.LastLimitTime = time.Now()
	rl.statsMutex.Unlock()
}

// Stats 返回统计信息快照
func (rl *RateLimitMiddleware) Stats() Stats {
	rl.statsMutex.RLock()
	defer rl.statsMutex.RUnlock()

	rl.mutex.RLock()
	activeLimiters := len(rl.limiters)
	rl.mutex.RUnlock()

	limiterStats := make(map[string]uint64, len(rl.stats.LimiterStats))
	for k, v := range rl.stats.LimiterStats {
		limiterStats[k] = v
	}
	return Stats{
		TotalRequests:   rl.stats.TotalRequests,
		AllowedRequests: rl.stats.AllowedRequests,
		LimitedRequests: rl.stats.LimitedRequests,
		ActiveLimiters:  activeLimiters,
		LimiterStats:    limiterStats,
		LastLimitTime:   rl.stats.LastLimitTime,
	}
}

// ResetStats 重置统计信息
func (rl *RateLimitMiddleware) ResetStats() {
	rl.statsMutex.Lock()
	defer rl.statsMutex.Unlock()
	rl.stats = Stats{LimiterStats: make(map[string]uint64)}
}

// LimitRate 返回限流率
func (rl *RateLimitMiddleware) LimitRate() float64 { return float64(rl.rate) }

// Burst 返回突发容量
func (rl *RateLimitMiddleware) Burst() int { return rl.burst }

// UpdateRate 运行时更新限流参数，同时刷新所有已存在的 limiter。
func (rl *RateLimitMiddleware) UpdateRate(newRate float64, newBurst int) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	rl.rate = rate.Limit(newRate)
	rl.burst = newBurst
	for _, entry := range rl.limiters {
		entry.limiter.SetLimit(rl.rate)
		entry.limiter.SetBurst(rl.burst)
	}
}

// ClearLimiters 立即清空所有 limiter（释放内存）
func (rl *RateLimitMiddleware) ClearLimiters() {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	rl.limiters = make(map[string]*limiterEntry)
}

// GetActiveLimiters 返回当前所有活跃 key 列表
func (rl *RateLimitMiddleware) GetActiveLimiters() []string {
	rl.mutex.RLock()
	defer rl.mutex.RUnlock()
	keys := make([]string, 0, len(rl.limiters))
	for key := range rl.limiters {
		keys = append(keys, key)
	}
	return keys
}

func (rl *RateLimitMiddleware) String() string {
	stats := rl.Stats()
	return fmt.Sprintf("RateLimitMiddleware[%s: rate=%.1f/s, burst=%d, total=%d, allowed=%d, limited=%d, active_limiters=%d]",
		rl.name, float64(rl.rate), rl.burst,
		stats.TotalRequests, stats.AllowedRequests, stats.LimitedRequests, stats.ActiveLimiters)
}

// IsRateLimitError 检查错误是否为限流相关错误
func IsRateLimitError(err error) bool {
	return errors.Is(err, ErrRateLimitExceeded) || errors.Is(err, ErrRateLimiterNotFound)
}
