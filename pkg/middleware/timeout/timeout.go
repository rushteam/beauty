package timeout

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/service/logger"
)

var (
	// ErrTimeout 超时错误
	ErrTimeout = errors.New("request timeout")
	// ErrTimeoutCanceled 请求被取消错误
	ErrTimeoutCanceled = errors.New("request canceled")
)

// Config 超时配置
type Config struct {
	// Name 超时控制器名称
	Name string
	// Timeout 超时时间
	Timeout time.Duration
	// SlowThreshold 慢请求阈值，超过此时间会记录警告日志
	SlowThreshold time.Duration
	// OnTimeout 超时时的回调函数
	OnTimeout func(name string, duration time.Duration)
	// OnSlow 慢请求时的回调函数
	OnSlow func(name string, duration time.Duration)
	// EnableMetrics 是否启用指标统计
	EnableMetrics bool
}

// DefaultConfig 返回默认的超时配置
func DefaultConfig(name string, timeout time.Duration) Config {
	return Config{
		Name:          name,
		Timeout:       timeout,
		SlowThreshold: timeout / 2, // 默认慢请求阈值为超时时间的一半
		EnableMetrics: true,
		OnTimeout: func(name string, duration time.Duration) {
			logger.Warn("request timeout",
				"name", name,
				"duration", duration.String(),
				"timeout", timeout.String())
		},
		OnSlow: func(name string, duration time.Duration) {
			logger.Warn("slow request detected",
				"name", name,
				"duration", duration.String(),
				"threshold", timeout.String())
		},
	}
}

// TimeoutController 超时控制器
type TimeoutController struct {
	name          string
	timeout       time.Duration
	slowThreshold time.Duration
	onTimeout     func(name string, duration time.Duration)
	onSlow        func(name string, duration time.Duration)
	enableMetrics bool

	// 统计信息
	mutex     sync.RWMutex
	stats     Stats
	lastReset time.Time
}

// Stats 统计信息
type Stats struct {
	TotalRequests   uint64        `json:"total_requests"`   // 总请求数
	TimeoutRequests uint64        `json:"timeout_requests"` // 超时请求数
	SlowRequests    uint64        `json:"slow_requests"`    // 慢请求数
	AvgDuration     time.Duration `json:"avg_duration"`     // 平均响应时间
	MaxDuration     time.Duration `json:"max_duration"`     // 最大响应时间
	MinDuration     time.Duration `json:"min_duration"`     // 最小响应时间
}

// NewTimeoutController 创建超时控制器
func NewTimeoutController(config Config) *TimeoutController {
	if config.Name == "" {
		config.Name = "timeout-controller"
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.SlowThreshold <= 0 {
		config.SlowThreshold = config.Timeout / 2
	}

	return &TimeoutController{
		name:          config.Name,
		timeout:       config.Timeout,
		slowThreshold: config.SlowThreshold,
		onTimeout:     config.OnTimeout,
		onSlow:        config.OnSlow,
		enableMetrics: config.EnableMetrics,
		lastReset:     time.Now(),
		stats: Stats{
			MinDuration: time.Duration(^uint64(0) >> 1), // 最大值作为初始最小值
		},
	}
}

// Execute 执行带超时控制的函数
func (tc *TimeoutController) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	start := time.Now()

	// 创建带超时的上下文
	timeoutCtx, cancel := context.WithTimeout(ctx, tc.timeout)
	defer cancel()

	// 使用通道来处理结果和超时
	errChan := make(chan error, 1)

	// 在goroutine中执行函数
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errChan <- fmt.Errorf("panic in timeout controller: %v", r)
			}
		}()
		errChan <- fn(timeoutCtx)
	}()

	// 等待结果或超时
	select {
	case err := <-errChan:
		duration := time.Since(start)
		tc.recordRequest(duration, false, err != nil)

		// 检查是否为慢请求
		if duration > tc.slowThreshold && tc.onSlow != nil {
			tc.onSlow(tc.name, duration)
		}

		return err
	case <-timeoutCtx.Done():
		duration := time.Since(start)
		tc.recordRequest(duration, true, true)

		if tc.onTimeout != nil {
			tc.onTimeout(tc.name, duration)
		}

		// 检查是取消还是超时
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return ErrTimeout
		}
		return ErrTimeoutCanceled
	}
}

// ExecuteWithResult 执行带超时控制和返回值的函数
func (tc *TimeoutController) ExecuteWithResult(ctx context.Context, fn func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	var result interface{}
	err := tc.Execute(ctx, func(ctx context.Context) error {
		var err error
		result, err = fn(ctx)
		return err
	})
	return result, err
}

// recordRequest 记录请求统计信息
func (tc *TimeoutController) recordRequest(duration time.Duration, isTimeout, isError bool) {
	if !tc.enableMetrics {
		return
	}

	tc.mutex.Lock()
	defer tc.mutex.Unlock()

	tc.stats.TotalRequests++

	if isTimeout {
		tc.stats.TimeoutRequests++
	}

	if duration > tc.slowThreshold {
		tc.stats.SlowRequests++
	}

	// 更新持续时间统计
	if duration > tc.stats.MaxDuration {
		tc.stats.MaxDuration = duration
	}
	if duration < tc.stats.MinDuration {
		tc.stats.MinDuration = duration
	}

	// 计算平均持续时间（简单移动平均）
	if tc.stats.TotalRequests == 1 {
		tc.stats.AvgDuration = duration
	} else {
		// 使用加权平均来避免溢出
		tc.stats.AvgDuration = time.Duration(
			(int64(tc.stats.AvgDuration)*int64(tc.stats.TotalRequests-1) + int64(duration)) /
				int64(tc.stats.TotalRequests))
	}
}

// Name 返回控制器名称
func (tc *TimeoutController) Name() string {
	return tc.name
}

// Timeout 返回超时时间
func (tc *TimeoutController) Timeout() time.Duration {
	return tc.timeout
}

// SlowThreshold 返回慢请求阈值
func (tc *TimeoutController) SlowThreshold() time.Duration {
	return tc.slowThreshold
}

// Stats 返回统计信息
func (tc *TimeoutController) Stats() Stats {
	tc.mutex.RLock()
	defer tc.mutex.RUnlock()

	// 创建副本以避免并发问题
	return Stats{
		TotalRequests:   tc.stats.TotalRequests,
		TimeoutRequests: tc.stats.TimeoutRequests,
		SlowRequests:    tc.stats.SlowRequests,
		AvgDuration:     tc.stats.AvgDuration,
		MaxDuration:     tc.stats.MaxDuration,
		MinDuration:     tc.stats.MinDuration,
	}
}

// ResetStats 重置统计信息
func (tc *TimeoutController) ResetStats() {
	tc.mutex.Lock()
	defer tc.mutex.Unlock()

	tc.stats = Stats{
		MinDuration: time.Duration(^uint64(0) >> 1),
	}
	tc.lastReset = time.Now()
}

// TimeoutRate 返回超时率
func (tc *TimeoutController) TimeoutRate() float64 {
	stats := tc.Stats()
	if stats.TotalRequests == 0 {
		return 0
	}
	return float64(stats.TimeoutRequests) / float64(stats.TotalRequests)
}

// SlowRate 返回慢请求率
func (tc *TimeoutController) SlowRate() float64 {
	stats := tc.Stats()
	if stats.TotalRequests == 0 {
		return 0
	}
	return float64(stats.SlowRequests) / float64(stats.TotalRequests)
}

// String 返回控制器的字符串表示
func (tc *TimeoutController) String() string {
	stats := tc.Stats()
	return fmt.Sprintf("TimeoutController[%s: timeout=%s, requests=%d, timeouts=%d, slow=%d, avg=%s]",
		tc.name, tc.timeout, stats.TotalRequests, stats.TimeoutRequests,
		stats.SlowRequests, stats.AvgDuration)
}
