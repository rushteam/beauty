package dlock

import (
	"fmt"
	"net/url"
	"sync"
)

// LockerFactory 从解析好的 URL 构造一个 Locker。scheme 由各 infra 子包在 init() 中注册。
type LockerFactory func(u *url.URL) (Locker, error)

// ElectorFactory 从解析好的 URL 构造一个 Elector。scheme 由各 infra 子包在 init() 中注册。
type ElectorFactory func(u *url.URL) (Elector, error)

var (
	factoryMu        sync.RWMutex
	lockerFactories  = make(map[string]LockerFactory)
	electorFactories = make(map[string]ElectorFactory)
)

// RegisterLocker 注册一个 scheme 对应的 Locker 工厂。
// 在各 infra 子包的 init() 中调用,重复注册同一 scheme 会 panic。
func RegisterLocker(scheme string, fn LockerFactory) {
	factoryMu.Lock()
	defer factoryMu.Unlock()
	if _, dup := lockerFactories[scheme]; dup {
		panic(fmt.Sprintf("dlock: locker factory for scheme %q already registered", scheme))
	}
	lockerFactories[scheme] = fn
}

// RegisterElector 注册一个 scheme 对应的 Elector 工厂。
// 在各 infra 子包的 init() 中调用,重复注册同一 scheme 会 panic。
func RegisterElector(scheme string, fn ElectorFactory) {
	factoryMu.Lock()
	defer factoryMu.Unlock()
	if _, dup := electorFactories[scheme]; dup {
		panic(fmt.Sprintf("dlock: elector factory for scheme %q already registered", scheme))
	}
	electorFactories[scheme] = fn
}

// New 根据 DSN 构造一个 Locker,与 conf.New 的 scheme 工厂模式对齐:先 import 对应
// 的 infra 子包(触发 init 注册工厂),再按 DSN 选择后端,免去在业务代码里硬编码
// 具体实现。锁的 key 在调用 Lock/TryLock 时传入,不在 DSN 里。
//
// 各后端 DSN 由其工厂决定,通用 query 约定:prefix(key 前缀)、ttl(会话/租约
// 存活时间,如 15s)。示例:
//
//	dlock.New("etcd://127.0.0.1:2379/?ttl=10s&prefix=/beauty/dlock/")
//	dlock.New("consul://127.0.0.1:8500/?ttl=15s")
//	dlock.New("redis://:pass@127.0.0.1:6379/0?ttl=15s&retry=100ms")
//
// 注:k8s 后端只提供 Elector(client-go 是选主语义,没有互斥锁原语),用 NewElector。
func New(rawURL string) (Locker, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("dlock: parse url: %w", err)
	}
	factoryMu.RLock()
	fn, ok := lockerFactories[u.Scheme]
	factoryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("dlock: unsupported locker scheme %q — import the matching infra package to register it", u.Scheme)
	}
	return fn(u)
}

// NewElector 根据 DSN 构造一个 Elector(选主器),用法同 New。除 etcd/consul/redis
// 外,k8s 后端也注册了 Elector(基于 Lease 资源),DSN 形如
// "k8s://?namespace=prod&kubeconfig=/path"。
func NewElector(rawURL string) (Elector, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("dlock: parse url: %w", err)
	}
	factoryMu.RLock()
	fn, ok := electorFactories[u.Scheme]
	factoryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("dlock: unsupported elector scheme %q — import the matching infra package to register it", u.Scheme)
	}
	return fn(u)
}
