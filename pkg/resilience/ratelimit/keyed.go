package ratelimit

import (
	"sync"
	"time"
)

// KeyedLimiter 按分组(group)维护独立的 Limiter 实例:每个 group 拥有自己的
// 令牌桶/滑动窗口,互不干扰。典型场景:多租户独立限流、按 API 分组限流。
//
// 借鉴 BullMQ Pro 的 Group Rate Limit:同一队列中不同 group 各自独立计数。
//
// 用法:
//
//	kl := NewKeyedLimiter(func(group string) Limiter {
//	    return NewTokenBucket(100, 10) // 每组:100 突发,10/s
//	})
//	allowed, retry := kl.Allow("tenant-A", "user-123")
//
// 并发安全。Stop 会停止所有子 limiter 的 gc(如果它们实现了 Stopper)。
type KeyedLimiter struct {
	factory    func(group string) Limiter
	maxIdle    time.Duration
	gcInterval time.Duration

	mu       sync.RWMutex
	limiters map[string]*groupEntry
	stop     chan struct{}
	once     sync.Once
}

type groupEntry struct {
	limiter  Limiter
	lastUsed time.Time
}

// Stopper 可选接口:拥有 Stop 方法的 Limiter 在被回收时会被调用。
type Stopper interface {
	Stop()
}

// KeyedOption 配置 KeyedLimiter。
type KeyedOption func(*keyedConfig)

type keyedConfig struct {
	maxIdle    time.Duration
	gcInterval time.Duration
}

// WithKeyedMaxIdle 设置分组 limiter 最大空闲时长(超过则回收)。默认 10min。
func WithKeyedMaxIdle(d time.Duration) KeyedOption {
	return func(c *keyedConfig) { c.maxIdle = d }
}

// WithKeyedGcInterval 设置 gc 扫描间隔。默认 2min。
func WithKeyedGcInterval(d time.Duration) KeyedOption {
	return func(c *keyedConfig) { c.gcInterval = d }
}

// NewKeyedLimiter 创建分组限流器。factory 按 group 名创建独立的 Limiter 实例。
func NewKeyedLimiter(factory func(group string) Limiter, opts ...KeyedOption) *KeyedLimiter {
	cfg := keyedConfig{maxIdle: 10 * time.Minute, gcInterval: 2 * time.Minute}
	for _, o := range opts {
		o(&cfg)
	}
	kl := &KeyedLimiter{
		factory:    factory,
		maxIdle:    cfg.maxIdle,
		gcInterval: cfg.gcInterval,
		limiters:   make(map[string]*groupEntry),
		stop:       make(chan struct{}),
	}
	go kl.gc()
	return kl
}

// Allow 对指定 group 下的 key 执行限流判定。group 决定使用哪个 Limiter 实例,
// key 在该 Limiter 内做具体判定。
func (kl *KeyedLimiter) Allow(group, key string) (allowed bool, retryAfter time.Duration) {
	l := kl.getOrCreate(group)
	return l.Allow(key)
}

// AllowGroup 仅按 group 限流(key 固定为空串)——适合"每组全局限流"场景。
func (kl *KeyedLimiter) AllowGroup(group string) (allowed bool, retryAfter time.Duration) {
	return kl.Allow(group, "")
}

// Groups 返回当前活跃的 group 数(近似)。
func (kl *KeyedLimiter) Groups() int {
	kl.mu.RLock()
	defer kl.mu.RUnlock()
	return len(kl.limiters)
}

// Stop 停止 gc 并清理所有子 limiter。幂等。
func (kl *KeyedLimiter) Stop() {
	kl.once.Do(func() {
		close(kl.stop)
		kl.mu.Lock()
		for _, e := range kl.limiters {
			if s, ok := e.limiter.(Stopper); ok {
				s.Stop()
			}
		}
		kl.limiters = make(map[string]*groupEntry)
		kl.mu.Unlock()
	})
}

func (kl *KeyedLimiter) getOrCreate(group string) Limiter {
	now := time.Now()
	kl.mu.RLock()
	if e, ok := kl.limiters[group]; ok {
		kl.mu.RUnlock()
		kl.mu.Lock()
		e.lastUsed = now
		kl.mu.Unlock()
		return e.limiter
	}
	kl.mu.RUnlock()

	kl.mu.Lock()
	defer kl.mu.Unlock()
	if e, ok := kl.limiters[group]; ok {
		e.lastUsed = now
		return e.limiter
	}
	l := kl.factory(group)
	kl.limiters[group] = &groupEntry{limiter: l, lastUsed: now}
	return l
}

func (kl *KeyedLimiter) gc() {
	ticker := time.NewTicker(kl.gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-kl.maxIdle)
			kl.mu.Lock()
			for g, e := range kl.limiters {
				if e.lastUsed.Before(cutoff) {
					if s, ok := e.limiter.(Stopper); ok {
						s.Stop()
					}
					delete(kl.limiters, g)
				}
			}
			kl.mu.Unlock()
		case <-kl.stop:
			return
		}
	}
}
