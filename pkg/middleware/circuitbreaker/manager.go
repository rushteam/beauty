package circuitbreaker

import (
	"fmt"
	"sync"

	"github.com/rushteam/beauty/pkg/logger"
)

// Manager 熔断器管理器，用于管理多个熔断器实例
type Manager struct {
	mutex    sync.RWMutex
	breakers map[string]*CircuitBreaker
	config   ManagerConfig
}

// ManagerConfig 管理器配置
type ManagerConfig struct {
	// DefaultConfig 默认的熔断器配置
	DefaultConfig Config
	// EnableLogging 是否启用日志
	EnableLogging bool
	// LogStateChange 是否记录状态变化
	LogStateChange bool
}

// NewManager 创建熔断器管理器
func NewManager(config ManagerConfig) *Manager {
	if config.DefaultConfig.Name == "" {
		config.DefaultConfig = DefaultConfig("default")
	}

	// 设置默认的状态变化回调
	if config.LogStateChange && config.DefaultConfig.OnStateChange == nil {
		config.DefaultConfig.OnStateChange = func(name string, from State, to State) {
			if config.EnableLogging {
				logger.Info("circuit breaker state changed",
					"name", name,
					"from", from.String(),
					"to", to.String())
			}
		}
	}

	return &Manager{
		breakers: make(map[string]*CircuitBreaker),
		config:   config,
	}
}

// GetOrCreate 获取或创建熔断器
func (m *Manager) GetOrCreate(name string, config ...Config) *CircuitBreaker {
	m.mutex.RLock()
	if cb, exists := m.breakers[name]; exists {
		m.mutex.RUnlock()
		return cb
	}
	m.mutex.RUnlock()

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// 双重检查
	if cb, exists := m.breakers[name]; exists {
		return cb
	}

	// 使用提供的配置或默认配置
	var cbConfig Config
	if len(config) > 0 {
		cbConfig = config[0]
	} else {
		cbConfig = m.config.DefaultConfig
	}
	cbConfig.Name = name

	// 如果没有设置状态变化回调，使用默认的
	if cbConfig.OnStateChange == nil && m.config.DefaultConfig.OnStateChange != nil {
		cbConfig.OnStateChange = m.config.DefaultConfig.OnStateChange
	}

	cb := NewCircuitBreaker(cbConfig)
	m.breakers[name] = cb

	if m.config.EnableLogging {
		logger.Info("circuit breaker created", "name", name)
	}

	return cb
}

// Get 获取熔断器
func (m *Manager) Get(name string) (*CircuitBreaker, bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	cb, exists := m.breakers[name]
	return cb, exists
}

// Remove 移除熔断器
func (m *Manager) Remove(name string) bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, exists := m.breakers[name]; exists {
		delete(m.breakers, name)
		if m.config.EnableLogging {
			logger.Info("circuit breaker removed", "name", name)
		}
		return true
	}
	return false
}

// List 列出所有熔断器名称
func (m *Manager) List() []string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	names := make([]string, 0, len(m.breakers))
	for name := range m.breakers {
		names = append(names, name)
	}
	return names
}

// Stats 获取所有熔断器的统计信息
func (m *Manager) Stats() map[string]CircuitBreakerStats {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	stats := make(map[string]CircuitBreakerStats)
	for name, cb := range m.breakers {
		stats[name] = CircuitBreakerStats{
			Name:   name,
			State:  cb.State(),
			Counts: cb.Counts(),
		}
	}
	return stats
}

// Reset 重置所有熔断器
func (m *Manager) Reset() {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, cb := range m.breakers {
		cb.Reset()
	}

	if m.config.EnableLogging {
		logger.Info("all circuit breakers reset")
	}
}

// ResetByName 重置指定名称的熔断器
func (m *Manager) ResetByName(name string) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if cb, exists := m.breakers[name]; exists {
		cb.Reset()
		if m.config.EnableLogging {
			logger.Info("circuit breaker reset", "name", name)
		}
		return true
	}
	return false
}

// CircuitBreakerStats 熔断器统计信息
type CircuitBreakerStats struct {
	Name   string `json:"name"`
	State  State  `json:"state"`
	Counts Counts `json:"counts"`
}

// String 返回统计信息的字符串表示
func (s CircuitBreakerStats) String() string {
	return fmt.Sprintf("CircuitBreaker[%s: %s, Requests: %d, Successes: %d, Failures: %d]",
		s.Name, s.State.String(), s.Counts.Requests, s.Counts.TotalSuccesses, s.Counts.TotalFailures)
}

// DefaultManager 默认的全局熔断器管理器
var DefaultManager = NewManager(ManagerConfig{
	DefaultConfig:  DefaultConfig("default"),
	EnableLogging:  true,
	LogStateChange: true,
})

// GetCircuitBreaker 从默认管理器获取或创建熔断器
func GetCircuitBreaker(name string, config ...Config) *CircuitBreaker {
	return DefaultManager.GetOrCreate(name, config...)
}

// GetCircuitBreakerStats 从默认管理器获取统计信息
func GetCircuitBreakerStats() map[string]CircuitBreakerStats {
	return DefaultManager.Stats()
}

// ResetCircuitBreaker 重置默认管理器中的熔断器
func ResetCircuitBreaker(name string) bool {
	return DefaultManager.ResetByName(name)
}

// ResetAllCircuitBreakers 重置默认管理器中的所有熔断器
func ResetAllCircuitBreakers() {
	DefaultManager.Reset()
}
