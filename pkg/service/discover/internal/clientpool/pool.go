// Package clientpool 按连接配置共享注册中心 SDK 客户端。
//
// 语义参照 net/http.Transport 的空闲连接复用：客户端在有使用方时保持，
// 最后一个使用方释放后再闲置 IdleTimeout 才真正关闭；闲置期间再次 Acquire 直接复用。
// 这样 Registry 本身不持有重资源，gRPC resolver 反复 Build/Close（idle 进出）、
// 多次 Dial 同一注册中心都只共享同一个 SDK 客户端，且不再使用时一定会被释放。
package clientpool

import (
	"sync"
	"time"
)

// DefaultIdleTimeout 与 http.DefaultTransport 的 IdleConnTimeout 一致。
const DefaultIdleTimeout = 90 * time.Second

// Pool 按 key 共享类型为 C 的客户端。零值不可用，需用 New 创建。
type Pool[C any] struct {
	idleTimeout time.Duration
	closeFn     func(C)

	mu      sync.Mutex
	entries map[string]*entry[C]
}

type entry[C any] struct {
	client C
	refs   int
	timer  *time.Timer
	gen    uint64 // 每次进入闲置自增，使过期的关闭定时器失效
}

// New 创建 Pool。closeFn 在客户端闲置超时后调用，且在锁外执行。
func New[C any](idleTimeout time.Duration, closeFn func(C)) *Pool[C] {
	return &Pool[C]{
		idleTimeout: idleTimeout,
		closeFn:     closeFn,
		entries:     make(map[string]*entry[C]),
	}
}

// Acquire 返回 key 对应的共享客户端，不存在时调用 create 创建。
// 调用方用完必须调用返回的 release；release 可重复调用，只有第一次生效。
// create 在池锁内执行，保证同一 key 只创建一次。
func (p *Pool[C]) Acquire(key string, create func() (C, error)) (C, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[key]
	if ok {
		if e.timer != nil {
			e.timer.Stop()
			e.timer = nil
		}
	} else {
		c, err := create()
		if err != nil {
			var zero C
			return zero, func() {}, err
		}
		e = &entry[C]{client: c}
		p.entries[key] = e
	}
	e.refs++
	var once sync.Once
	return e.client, func() { once.Do(func() { p.release(key, e) }) }, nil
}

func (p *Pool[C]) release(key string, e *entry[C]) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e.refs--
	if e.refs > 0 {
		return
	}
	e.gen++
	gen := e.gen
	e.timer = time.AfterFunc(p.idleTimeout, func() { p.expire(key, e, gen) })
}

func (p *Pool[C]) expire(key string, e *entry[C], gen uint64) {
	p.mu.Lock()
	if e.refs > 0 || e.gen != gen || p.entries[key] != e {
		p.mu.Unlock()
		return
	}
	delete(p.entries, key)
	p.mu.Unlock()
	p.closeFn(e.client)
}

// Len 返回当前池中（含闲置中）的客户端数量。
func (p *Pool[C]) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}
