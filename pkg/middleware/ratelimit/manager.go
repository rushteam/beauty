package ratelimit

import (
	"context"
	"fmt"
	"sync"

	"github.com/rushteam/beauty/pkg/service/logger"
)

// Manager 限流中间件管理器
type Manager struct {
	mutex       sync.RWMutex
	middlewares map[string]*RateLimitMiddleware
	config      ManagerConfig
}

// ManagerConfig 管理器配置
type ManagerConfig struct {
	// DefaultRate 默认限流速率
	DefaultRate float64
	// DefaultBurst 默认突发容量
	DefaultBurst int
	// DefaultKeyExtractor 默认键提取器
	DefaultKeyExtractor KeyExtractor
	// EnableLogging 是否启用日志
	EnableLogging bool
	// EnableMetrics 是否启用指标统计
	EnableMetrics bool
}

// NewManager 创建限流中间件管理器
func NewManager(config ManagerConfig) *Manager {
	if config.DefaultRate <= 0 {
		config.DefaultRate = 100
	}
	if config.DefaultBurst <= 0 {
		config.DefaultBurst = int(config.DefaultRate)
	}
	if config.DefaultKeyExtractor == nil {
		config.DefaultKeyExtractor = NewIPKeyExtractor()
	}

	return &Manager{
		middlewares: make(map[string]*RateLimitMiddleware),
		config:      config,
	}
}

// GetOrCreate 获取或创建限流中间件
func (m *Manager) GetOrCreate(name string, rate ...float64) *RateLimitMiddleware {
	m.mutex.RLock()
	if rl, exists := m.middlewares[name]; exists {
		m.mutex.RUnlock()
		return rl
	}
	m.mutex.RUnlock()

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// 双重检查
	if rl, exists := m.middlewares[name]; exists {
		return rl
	}

	// 使用提供的速率或默认值
	rateLimitRate := m.config.DefaultRate
	if len(rate) > 0 && rate[0] > 0 {
		rateLimitRate = rate[0]
	}

	config := Config{
		Name:          name,
		Rate:          rateLimitRate,
		Burst:         m.config.DefaultBurst,
		KeyExtractor:  m.config.DefaultKeyExtractor,
		EnableMetrics: m.config.EnableMetrics,
		DefaultKey:    fmt.Sprintf("%s-default", name),
	}

	// 设置回调函数
	if m.config.EnableLogging {
		config.OnRateLimit = func(ctx context.Context, key string, rate float64) {
			logger.Warn("rate limit exceeded",
				"name", name,
				"key", key,
				"rate", rate)
		}
	}

	rl := NewRateLimitMiddleware(config)
	m.middlewares[name] = rl

	if m.config.EnableLogging {
		logger.Info("rate limit middleware created",
			"name", name,
			"rate", rateLimitRate,
			"burst", m.config.DefaultBurst)
	}

	return rl
}

// Get 获取限流中间件
func (m *Manager) Get(name string) (*RateLimitMiddleware, bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	rl, exists := m.middlewares[name]
	return rl, exists
}

// Remove 移除限流中间件
func (m *Manager) Remove(name string) bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, exists := m.middlewares[name]; exists {
		delete(m.middlewares, name)
		if m.config.EnableLogging {
			logger.Info("rate limit middleware removed", "name", name)
		}
		return true
	}
	return false
}

// List 列出所有限流中间件名称
func (m *Manager) List() []string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	names := make([]string, 0, len(m.middlewares))
	for name := range m.middlewares {
		names = append(names, name)
	}
	return names
}

// Stats 获取所有限流中间件的统计信息
func (m *Manager) Stats() map[string]RateLimitStats {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	stats := make(map[string]RateLimitStats)
	for name, rl := range m.middlewares {
		stats[name] = RateLimitStats{
			Name:           name,
			Rate:           rl.LimitRate(),
			Burst:          rl.Burst(),
			Stats:          rl.Stats(),
			ActiveLimiters: rl.GetActiveLimiters(),
		}
	}
	return stats
}

// ResetStats 重置所有限流中间件的统计信息
func (m *Manager) ResetStats() {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, rl := range m.middlewares {
		rl.ResetStats()
	}

	if m.config.EnableLogging {
		logger.Info("all rate limit middleware stats reset")
	}
}

// ResetStatsByName 重置指定名称的限流中间件统计信息
func (m *Manager) ResetStatsByName(name string) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if rl, exists := m.middlewares[name]; exists {
		rl.ResetStats()
		if m.config.EnableLogging {
			logger.Info("rate limit middleware stats reset", "name", name)
		}
		return true
	}
	return false
}

// UpdateRate 更新指定中间件的限流率
func (m *Manager) UpdateRate(name string, newRate float64, newBurst int) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if rl, exists := m.middlewares[name]; exists {
		rl.UpdateRate(newRate, newBurst)
		if m.config.EnableLogging {
			logger.Info("rate limit updated",
				"name", name,
				"new_rate", newRate,
				"new_burst", newBurst)
		}
		return true
	}
	return false
}

// RateLimitStats 限流统计信息
type RateLimitStats struct {
	Name           string   `json:"name"`
	Rate           float64  `json:"rate"`
	Burst          int      `json:"burst"`
	Stats          Stats    `json:"stats"`
	ActiveLimiters []string `json:"active_limiters"`
}

// String 返回统计信息的字符串表示
func (s RateLimitStats) String() string {
	return fmt.Sprintf("RateLimit[%s: rate=%.1f/s, burst=%d, total=%d, allowed=%d, limited=%d, active=%d]",
		s.Name, s.Rate, s.Burst, s.Stats.TotalRequests, s.Stats.AllowedRequests,
		s.Stats.LimitedRequests, len(s.ActiveLimiters))
}

// DefaultManager 默认的全局限流管理器
var DefaultManager = NewManager(ManagerConfig{
	DefaultRate:         100,
	DefaultBurst:        100,
	DefaultKeyExtractor: NewIPKeyExtractor(),
	EnableLogging:       true,
	EnableMetrics:       true,
})

// GetRateLimitMiddleware 从默认管理器获取或创建限流中间件
func GetRateLimitMiddleware(name string, rate ...float64) *RateLimitMiddleware {
	return DefaultManager.GetOrCreate(name, rate...)
}

// GetRateLimitStats 从默认管理器获取统计信息
func GetRateLimitStats() map[string]RateLimitStats {
	return DefaultManager.Stats()
}

// ResetRateLimitStats 重置默认管理器中的限流中间件统计信息
func ResetRateLimitStats(name string) bool {
	return DefaultManager.ResetStatsByName(name)
}

// ResetAllRateLimitStats 重置默认管理器中的所有限流中间件统计信息
func ResetAllRateLimitStats() {
	DefaultManager.ResetStats()
}

// UpdateRateLimit 更新默认管理器中的限流率
func UpdateRateLimit(name string, newRate float64, newBurst int) bool {
	return DefaultManager.UpdateRate(name, newRate, newBurst)
}
