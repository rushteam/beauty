package circuitbreaker

import (
	"time"
)

// DefaultConfig 返回默认的熔断器配置
func DefaultConfig(name string) Config {
	return Config{
		Name:        name,
		MaxRequests: 5,
		Interval:    time.Minute,
		Timeout:     time.Minute,
		ReadyToTrip: func(counts Counts) bool {
			// 当请求数超过 10 且失败率超过 50% 时触发熔断
			return counts.Requests >= 10 &&
				float64(counts.TotalFailures)/float64(counts.Requests) > 0.5
		},
	}
}

// HighSensitivityConfig 返回高敏感度的熔断器配置（更容易触发熔断）
func HighSensitivityConfig(name string) Config {
	return Config{
		Name:        name,
		MaxRequests: 3,
		Interval:    30 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts Counts) bool {
			// 当请求数超过 5 且失败率超过 30% 时触发熔断
			return counts.Requests >= 5 &&
				float64(counts.TotalFailures)/float64(counts.Requests) > 0.3
		},
	}
}

// LowSensitivityConfig 返回低敏感度的熔断器配置（不容易触发熔断）
func LowSensitivityConfig(name string) Config {
	return Config{
		Name:        name,
		MaxRequests: 10,
		Interval:    2 * time.Minute,
		Timeout:     2 * time.Minute,
		ReadyToTrip: func(counts Counts) bool {
			// 当请求数超过 20 且失败率超过 70% 时触发熔断
			return counts.Requests >= 20 &&
				float64(counts.TotalFailures)/float64(counts.Requests) > 0.7
		},
	}
}

// ConsecutiveFailuresConfig 返回基于连续失败次数的熔断器配置
func ConsecutiveFailuresConfig(name string, maxFailures uint32) Config {
	return Config{
		Name:        name,
		MaxRequests: 5,
		Interval:    time.Minute,
		Timeout:     time.Minute,
		ReadyToTrip: func(counts Counts) bool {
			// 当连续失败次数超过指定值时触发熔断
			return counts.ConsecutiveFailures >= maxFailures
		},
	}
}

// CustomConfig 自定义配置构建器
type CustomConfig struct {
	config Config
}

// NewCustomConfig 创建自定义配置构建器
func NewCustomConfig(name string) *CustomConfig {
	return &CustomConfig{
		config: DefaultConfig(name),
	}
}

// WithMaxRequests 设置半开状态下的最大请求数
func (c *CustomConfig) WithMaxRequests(maxRequests uint32) *CustomConfig {
	c.config.MaxRequests = maxRequests
	return c
}

// WithInterval 设置统计窗口时间间隔
func (c *CustomConfig) WithInterval(interval time.Duration) *CustomConfig {
	c.config.Interval = interval
	return c
}

// WithTimeout 设置熔断器开启后的超时时间
func (c *CustomConfig) WithTimeout(timeout time.Duration) *CustomConfig {
	c.config.Timeout = timeout
	return c
}

// WithReadyToTrip 设置熔断判断函数
func (c *CustomConfig) WithReadyToTrip(readyToTrip func(counts Counts) bool) *CustomConfig {
	c.config.ReadyToTrip = readyToTrip
	return c
}

// WithOnStateChange 设置状态变化回调函数
func (c *CustomConfig) WithOnStateChange(onStateChange func(name string, from State, to State)) *CustomConfig {
	c.config.OnStateChange = onStateChange
	return c
}

// Build 构建配置
func (c *CustomConfig) Build() Config {
	return c.config
}
