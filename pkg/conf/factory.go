package conf

import (
	"fmt"
	"net/url"
	"sync"
)

// FactoryFunc 从解析好的 URL 构造一个 ConfigCenter。
// scheme 由各 infra 包在 init() 中注册。
type FactoryFunc func(u *url.URL) (ConfigCenter, error)

var (
	factoryMu sync.RWMutex
	factories = make(map[string]FactoryFunc)
)

// RegisterFactory 注册一个 scheme 对应的 ConfigCenter 工厂。
// 在各 infra 子包的 init() 中调用，重复注册同一 scheme 会 panic。
func RegisterFactory(scheme string, fn FactoryFunc) {
	factoryMu.Lock()
	defer factoryMu.Unlock()
	if _, dup := factories[scheme]; dup {
		panic(fmt.Sprintf("conf: factory for scheme %q already registered", scheme))
	}
	factories[scheme] = fn
}

func lookupFactory(scheme string) (FactoryFunc, bool) {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	fn, ok := factories[scheme]
	return fn, ok
}
