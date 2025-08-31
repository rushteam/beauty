package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	// Allow 检查是否允许请求通过
	Allow() bool
	// Wait 等待直到可以处理请求
	Wait(ctx context.Context) error
	// Reserve 预留令牌，返回预留信息
	Reserve() *rate.Reservation
	// Limit 返回当前限制速率
	Limit() rate.Limit
	// Burst 返回突发容量
	Burst() int
}

// KeyExtractor 键提取器接口，用于从请求中提取限流键
type KeyExtractor interface {
	// Extract 从请求元数据中提取限流键
	Extract(ctx context.Context, metadata map[string]interface{}) (string, error)
}

// Config 限流配置
type Config struct {
	// Name 限流器名称
	Name string
	// Rate 每秒允许的请求数
	Rate float64
	// Burst 突发容量
	Burst int
	// KeyExtractor 键提取器
	KeyExtractor KeyExtractor
	// EnableMetrics 是否启用指标统计
	EnableMetrics bool
	// OnRateLimit 限流时的回调函数
	OnRateLimit func(ctx context.Context, key string, rate float64)
	// DefaultKey 默认限流键（当提取器无法提取键时使用）
	DefaultKey string
}

// RateLimitMiddleware 限流中间件
type RateLimitMiddleware struct {
	name          string
	keyExtractor  KeyExtractor
	defaultKey    string
	enableMetrics bool
	onRateLimit   func(ctx context.Context, key string, rate float64)

	// 限流器管理
	mutex    sync.RWMutex
	limiters map[string]*rate.Limiter
	rate     rate.Limit
	burst    int

	// 统计信息
	statsMutex sync.RWMutex
	stats      Stats
}

// Stats 限流统计信息
type Stats struct {
	TotalRequests   uint64            `json:"total_requests"`   // 总请求数
	AllowedRequests uint64            `json:"allowed_requests"` // 允许通过的请求数
	LimitedRequests uint64            `json:"limited_requests"` // 被限流的请求数
	ActiveLimiters  int               `json:"active_limiters"`  // 活跃限流器数量
	LimiterStats    map[string]uint64 `json:"limiter_stats"`    // 每个键的限流统计
	LastLimitTime   time.Time         `json:"last_limit_time"`  // 最后限流时间
}

// NewRateLimitMiddleware 创建限流中间件
func NewRateLimitMiddleware(config Config) *RateLimitMiddleware {
	if config.Name == "" {
		config.Name = "rate-limit-middleware"
	}
	if config.Rate <= 0 {
		config.Rate = 100 // 默认每秒100个请求
	}
	if config.Burst <= 0 {
		config.Burst = int(config.Rate) // 默认突发容量等于速率
	}
	if config.DefaultKey == "" {
		config.DefaultKey = "default"
	}

	return &RateLimitMiddleware{
		name:          config.Name,
		keyExtractor:  config.KeyExtractor,
		defaultKey:    config.DefaultKey,
		enableMetrics: config.EnableMetrics,
		onRateLimit:   config.OnRateLimit,
		limiters:      make(map[string]*rate.Limiter),
		rate:          rate.Limit(config.Rate),
		burst:         config.Burst,
		stats: Stats{
			LimiterStats: make(map[string]uint64),
		},
	}
}

// Name 返回中间件名称
func (rl *RateLimitMiddleware) Name() string {
	return rl.name
}

// Allow 检查请求是否允许通过
func (rl *RateLimitMiddleware) Allow(ctx context.Context, metadata map[string]interface{}) error {
	key := rl.extractKey(ctx, metadata)
	limiter := rl.getLimiter(key)

	rl.recordRequest()

	if limiter.Allow() {
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
func (rl *RateLimitMiddleware) Wait(ctx context.Context, metadata map[string]interface{}) error {
	key := rl.extractKey(ctx, metadata)
	limiter := rl.getLimiter(key)

	rl.recordRequest()

	err := limiter.Wait(ctx)
	if err != nil {
		rl.recordLimited(key)
		if rl.onRateLimit != nil {
			rl.onRateLimit(ctx, key, float64(rl.rate))
		}
		return err
	}

	rl.recordAllowed(key)
	return nil
}

// extractKey 提取限流键
func (rl *RateLimitMiddleware) extractKey(ctx context.Context, metadata map[string]interface{}) string {
	if rl.keyExtractor != nil {
		if key, err := rl.keyExtractor.Extract(ctx, metadata); err == nil {
			return key
		}
	}
	return rl.defaultKey
}

// getLimiter 获取或创建限流器
func (rl *RateLimitMiddleware) getLimiter(key string) *rate.Limiter {
	rl.mutex.RLock()
	limiter, exists := rl.limiters[key]
	rl.mutex.RUnlock()

	if exists {
		return limiter
	}

	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	// 双重检查
	if limiter, exists := rl.limiters[key]; exists {
		return limiter
	}

	// 创建新的限流器
	limiter = rate.NewLimiter(rl.rate, rl.burst)
	rl.limiters[key] = limiter
	return limiter
}

// recordRequest 记录请求
func (rl *RateLimitMiddleware) recordRequest() {
	if !rl.enableMetrics {
		return
	}

	rl.statsMutex.Lock()
	defer rl.statsMutex.Unlock()
	rl.stats.TotalRequests++
}

// recordAllowed 记录允许通过的请求
func (rl *RateLimitMiddleware) recordAllowed(key string) {
	if !rl.enableMetrics {
		return
	}

	rl.statsMutex.Lock()
	defer rl.statsMutex.Unlock()
	rl.stats.AllowedRequests++
}

// recordLimited 记录被限流的请求
func (rl *RateLimitMiddleware) recordLimited(key string) {
	if !rl.enableMetrics {
		return
	}

	rl.statsMutex.Lock()
	defer rl.statsMutex.Unlock()
	rl.stats.LimitedRequests++
	rl.stats.LimiterStats[key]++
	rl.stats.LastLimitTime = time.Now()
}

// Stats 返回统计信息
func (rl *RateLimitMiddleware) Stats() Stats {
	rl.statsMutex.RLock()
	defer rl.statsMutex.RUnlock()

	rl.mutex.RLock()
	activeLimiters := len(rl.limiters)
	rl.mutex.RUnlock()

	// 创建副本
	limiterStats := make(map[string]uint64)
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
	rl.stats = Stats{
		LimiterStats: make(map[string]uint64),
	}
}

// LimitRate 返回限流率
func (rl *RateLimitMiddleware) LimitRate() float64 {
	return float64(rl.rate)
}

// Burst 返回突发容量
func (rl *RateLimitMiddleware) Burst() int {
	return rl.burst
}

// UpdateRate 更新限流率
func (rl *RateLimitMiddleware) UpdateRate(newRate float64, newBurst int) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	rl.rate = rate.Limit(newRate)
	rl.burst = newBurst

	// 更新所有现有限流器
	for _, limiter := range rl.limiters {
		limiter.SetLimit(rl.rate)
		limiter.SetBurst(rl.burst)
	}
}

// ClearLimiters 清除所有限流器（用于释放内存）
func (rl *RateLimitMiddleware) ClearLimiters() {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()
	rl.limiters = make(map[string]*rate.Limiter)
}

// GetActiveLimiters 获取活跃限流器列表
func (rl *RateLimitMiddleware) GetActiveLimiters() []string {
	rl.mutex.RLock()
	defer rl.mutex.RUnlock()

	keys := make([]string, 0, len(rl.limiters))
	for key := range rl.limiters {
		keys = append(keys, key)
	}
	return keys
}

// String 返回中间件的字符串表示
func (rl *RateLimitMiddleware) String() string {
	stats := rl.Stats()
	limitRate := rl.LimitRate()
	return fmt.Sprintf("RateLimitMiddleware[%s: rate=%.1f/s, burst=%d, total=%d, allowed=%d, limited=%d, active_limiters=%d]",
		rl.name, limitRate, rl.burst, stats.TotalRequests, stats.AllowedRequests,
		stats.LimitedRequests, stats.ActiveLimiters)
}

// IsRateLimitError 检查错误是否为限流相关错误
func IsRateLimitError(err error) bool {
	return err == ErrRateLimitExceeded || err == ErrRateLimiterNotFound
}
