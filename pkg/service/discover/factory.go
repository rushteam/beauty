package discover

import (
	"fmt"
	"net/url"
	"sync"
)

// RegistryFactory 注册中心工厂接口
type RegistryFactory interface {
	// Scheme 返回支持的协议方案
	Scheme() string

	// CreateFromURL 从URL创建注册中心实例
	CreateFromURL(targetURL *url.URL) (Discovery, error)

	// CreateFromConfig 从配置创建注册中心实例
	CreateFromConfig(config interface{}) (Discovery, error)
}

// RegistryFactoryFunc 工厂函数类型
type RegistryFactoryFunc func(targetURL *url.URL) (Discovery, error)

// registryFactory 内部工厂实现
type registryFactory struct {
	scheme string
	fn     RegistryFactoryFunc
}

func (f *registryFactory) Scheme() string {
	return f.scheme
}

func (f *registryFactory) CreateFromURL(targetURL *url.URL) (Discovery, error) {
	return f.fn(targetURL)
}

func (f *registryFactory) CreateFromConfig(config interface{}) (Discovery, error) {
	// 默认实现不支持从配置创建，子类可以重写
	return nil, fmt.Errorf("CreateFromConfig not supported for scheme %s", f.scheme)
}

// RegistryManager 注册中心管理器
type RegistryManager struct {
	factories map[string]RegistryFactory
	mu        sync.RWMutex
}

var (
	globalManager = &RegistryManager{
		factories: make(map[string]RegistryFactory),
	}
)

// RegisterFactory 注册工厂
func RegisterFactory(factory RegistryFactory) {
	globalManager.RegisterFactory(factory)
}

// RegisterFactoryFunc 注册工厂函数
func RegisterFactoryFunc(scheme string, fn RegistryFactoryFunc) {
	globalManager.RegisterFactory(&registryFactory{
		scheme: scheme,
		fn:     fn,
	})
}

// GetManager 获取全局管理器实例
func GetManager() *RegistryManager {
	return globalManager
}

// RegisterFactory 注册工厂
func (m *RegistryManager) RegisterFactory(factory RegistryFactory) {
	m.mu.Lock()
	defer m.mu.Unlock()

	scheme := factory.Scheme()
	if scheme == "" {
		panic("registry factory scheme cannot be empty")
	}

	if _, exists := m.factories[scheme]; exists {
		panic(fmt.Sprintf("registry factory for scheme %s already registered", scheme))
	}

	m.factories[scheme] = factory
}

// RegisterFactoryFunc 注册工厂函数
func (m *RegistryManager) RegisterFactoryFunc(scheme string, fn RegistryFactoryFunc) {
	m.RegisterFactory(&registryFactory{
		scheme: scheme,
		fn:     fn,
	})
}

// CreateRegistry 创建注册中心实例
func (m *RegistryManager) CreateRegistry(target string) (Discovery, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid target URL %s: %w", target, err)
	}

	scheme := targetURL.Scheme
	if scheme == "" {
		return nil, fmt.Errorf("target URL must have a scheme: %s", target)
	}

	m.mu.RLock()
	factory, exists := m.factories[scheme]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unsupported registry scheme: %s, available schemes: %v",
			scheme, m.getAvailableSchemes())
	}

	return factory.CreateFromURL(targetURL)
}

// CreateRegistryFromURL 从URL创建注册中心实例
func (m *RegistryManager) CreateRegistryFromURL(targetURL *url.URL) (Discovery, error) {
	scheme := targetURL.Scheme
	if scheme == "" {
		return nil, fmt.Errorf("URL must have a scheme")
	}

	m.mu.RLock()
	factory, exists := m.factories[scheme]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unsupported registry scheme: %s, available schemes: %v",
			scheme, m.getAvailableSchemes())
	}

	return factory.CreateFromURL(targetURL)
}

// GetAvailableSchemes 获取所有可用的协议方案
func (m *RegistryManager) GetAvailableSchemes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.getAvailableSchemes()
}

func (m *RegistryManager) getAvailableSchemes() []string {
	schemes := make([]string, 0, len(m.factories))
	for scheme := range m.factories {
		schemes = append(schemes, scheme)
	}
	return schemes
}

// IsSchemeSupported 检查是否支持某个协议方案
func (m *RegistryManager) IsSchemeSupported(scheme string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.factories[scheme]
	return exists
}

// UnregisterFactory 注销工厂
func (m *RegistryManager) UnregisterFactory(scheme string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.factories, scheme)
}

// ClearFactories 清空所有工厂
func (m *RegistryManager) ClearFactories() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.factories = make(map[string]RegistryFactory)
}
