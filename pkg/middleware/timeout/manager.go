package timeout

import (
	"fmt"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/logger"
)

// Manager 超时控制器管理器，用于管理多个超时控制器实例
type Manager struct {
	mutex       sync.RWMutex
	controllers map[string]*TimeoutController
	config      ManagerConfig
}

// ManagerConfig 管理器配置
type ManagerConfig struct {
	// DefaultTimeout 默认超时时间
	DefaultTimeout time.Duration
	// DefaultSlowThreshold 默认慢请求阈值
	DefaultSlowThreshold time.Duration
	// EnableLogging 是否启用日志
	EnableLogging bool
	// EnableMetrics 是否启用指标统计
	EnableMetrics bool
}

// NewManager 创建超时控制器管理器
func NewManager(config ManagerConfig) *Manager {
	if config.DefaultTimeout <= 0 {
		config.DefaultTimeout = 30 * time.Second
	}
	if config.DefaultSlowThreshold <= 0 {
		config.DefaultSlowThreshold = config.DefaultTimeout / 2
	}

	return &Manager{
		controllers: make(map[string]*TimeoutController),
		config:      config,
	}
}

// GetOrCreate 获取或创建超时控制器
func (m *Manager) GetOrCreate(name string, timeout ...time.Duration) *TimeoutController {
	m.mutex.RLock()
	if tc, exists := m.controllers[name]; exists {
		m.mutex.RUnlock()
		return tc
	}
	m.mutex.RUnlock()

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// 双重检查
	if tc, exists := m.controllers[name]; exists {
		return tc
	}

	// 使用提供的超时时间或默认值
	timeoutDuration := m.config.DefaultTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		timeoutDuration = timeout[0]
	}

	config := Config{
		Name:          name,
		Timeout:       timeoutDuration,
		SlowThreshold: m.config.DefaultSlowThreshold,
		EnableMetrics: m.config.EnableMetrics,
	}

	// 设置回调函数
	if m.config.EnableLogging {
		config.OnTimeout = func(name string, duration time.Duration) {
			logger.Warn("request timeout",
				"name", name,
				"duration", duration.String(),
				"timeout", timeoutDuration.String())
		}
		config.OnSlow = func(name string, duration time.Duration) {
			logger.Warn("slow request detected",
				"name", name,
				"duration", duration.String(),
				"threshold", m.config.DefaultSlowThreshold.String())
		}
	}

	tc := NewTimeoutController(config)
	m.controllers[name] = tc

	if m.config.EnableLogging {
		logger.Info("timeout controller created",
			"name", name,
			"timeout", timeoutDuration.String())
	}

	return tc
}

// Get 获取超时控制器
func (m *Manager) Get(name string) (*TimeoutController, bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	tc, exists := m.controllers[name]
	return tc, exists
}

// Remove 移除超时控制器
func (m *Manager) Remove(name string) bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, exists := m.controllers[name]; exists {
		delete(m.controllers, name)
		if m.config.EnableLogging {
			logger.Info("timeout controller removed", "name", name)
		}
		return true
	}
	return false
}

// List 列出所有超时控制器名称
func (m *Manager) List() []string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	names := make([]string, 0, len(m.controllers))
	for name := range m.controllers {
		names = append(names, name)
	}
	return names
}

// Stats 获取所有超时控制器的统计信息
func (m *Manager) Stats() map[string]TimeoutControllerStats {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	stats := make(map[string]TimeoutControllerStats)
	for name, tc := range m.controllers {
		stats[name] = TimeoutControllerStats{
			Name:          name,
			Timeout:       tc.Timeout(),
			SlowThreshold: tc.SlowThreshold(),
			Stats:         tc.Stats(),
			TimeoutRate:   tc.TimeoutRate(),
			SlowRate:      tc.SlowRate(),
		}
	}
	return stats
}

// ResetStats 重置所有超时控制器的统计信息
func (m *Manager) ResetStats() {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, tc := range m.controllers {
		tc.ResetStats()
	}

	if m.config.EnableLogging {
		logger.Info("all timeout controller stats reset")
	}
}

// ResetStatsByName 重置指定名称的超时控制器统计信息
func (m *Manager) ResetStatsByName(name string) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if tc, exists := m.controllers[name]; exists {
		tc.ResetStats()
		if m.config.EnableLogging {
			logger.Info("timeout controller stats reset", "name", name)
		}
		return true
	}
	return false
}

// TimeoutControllerStats 超时控制器统计信息
type TimeoutControllerStats struct {
	Name          string        `json:"name"`
	Timeout       time.Duration `json:"timeout"`
	SlowThreshold time.Duration `json:"slow_threshold"`
	Stats         Stats         `json:"stats"`
	TimeoutRate   float64       `json:"timeout_rate"`
	SlowRate      float64       `json:"slow_rate"`
}

// String 返回统计信息的字符串表示
func (s TimeoutControllerStats) String() string {
	return fmt.Sprintf("TimeoutController[%s: timeout=%s, requests=%d, timeouts=%d(%.2f%%), slow=%d(%.2f%%), avg=%s]",
		s.Name, s.Timeout, s.Stats.TotalRequests, s.Stats.TimeoutRequests, s.TimeoutRate*100,
		s.Stats.SlowRequests, s.SlowRate*100, s.Stats.AvgDuration)
}

// DefaultManager 默认的全局超时控制器管理器
var DefaultManager = NewManager(ManagerConfig{
	DefaultTimeout:       30 * time.Second,
	DefaultSlowThreshold: 15 * time.Second,
	EnableLogging:        true,
	EnableMetrics:        true,
})

// GetTimeoutController 从默认管理器获取或创建超时控制器
func GetTimeoutController(name string, timeout ...time.Duration) *TimeoutController {
	return DefaultManager.GetOrCreate(name, timeout...)
}

// GetTimeoutControllerStats 从默认管理器获取统计信息
func GetTimeoutControllerStats() map[string]TimeoutControllerStats {
	return DefaultManager.Stats()
}

// ResetTimeoutControllerStats 重置默认管理器中的超时控制器统计信息
func ResetTimeoutControllerStats(name string) bool {
	return DefaultManager.ResetStatsByName(name)
}

// ResetAllTimeoutControllerStats 重置默认管理器中的所有超时控制器统计信息
func ResetAllTimeoutControllerStats() {
	DefaultManager.ResetStats()
}
