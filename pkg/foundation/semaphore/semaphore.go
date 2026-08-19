// Package semaphore 提供加权信号量原语:限制对共享资源的最大并发占用量。
//
// 与 channel-based 等权信号量不同,本包支持**加权获取**:每次 Acquire 可指定 cost,
// 适用于"重操作占 5 槽、轻操作占 1 槽"的场景。等权场景(bulkhead/舱壁隔离)
// 是 cost=1 的特殊情况,直接用 Do(ctx, fn) 即可。
//
// 与 ratelimit(限速率/每秒多少) 互补: semaphore 限的是**同时占用**的容量。
// 与 circuitbreaker(按错误率熔断) 互补: semaphore 在错误率未达阈值时就能限并发。
//
// 纯标准库、并发安全。
package semaphore

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrFull 表示信号量已满,请求被拒绝。
var ErrFull = errors.New("semaphore: full")

// Option 配置 Semaphore。
type Option func(*config)

type config struct {
	capacity int64
	maxWait  time.Duration
	onReject func()
}

// WithCapacity 设置总容量(默认 10)。
func WithCapacity(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.capacity = n
		}
	}
}

// WithMaxWait 设置等待获取的最大时间(默认 0,即满则立即拒绝)。
func WithMaxWait(d time.Duration) Option {
	return func(c *config) {
		if d >= 0 {
			c.maxWait = d
		}
	}
}

// WithOnReject 注册被拒绝时的回调(用于计数/告警)。
func WithOnReject(fn func()) Option {
	return func(c *config) { c.onReject = fn }
}

// Semaphore 加权信号量。并发安全。
type Semaphore struct {
	cfg      config
	mu       sync.Mutex
	cond     *sync.Cond
	used     int64
	inFlight atomic.Int64
}

// New 创建信号量。
func New(opts ...Option) *Semaphore {
	cfg := config{capacity: 10}
	for _, o := range opts {
		o(&cfg)
	}
	s := &Semaphore{cfg: cfg}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Acquire 获取 cost 个单位的容量。阻塞直到可用、超时或 context 取消。
// cost 必须 > 0 且 <= capacity,否则 panic。
func (s *Semaphore) Acquire(ctx context.Context, cost int64) error {
	if cost <= 0 || cost > s.cfg.capacity {
		panic("semaphore: invalid cost")
	}

	s.mu.Lock()
	if s.used+cost <= s.cfg.capacity {
		s.used += cost
		s.inFlight.Add(cost)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// 需要等待
	if s.cfg.maxWait == 0 {
		if s.cfg.onReject != nil {
			s.cfg.onReject()
		}
		return ErrFull
	}

	return s.acquireWait(ctx, cost)
}

// TryAcquire 非阻塞尝试获取。成功返回 true。
func (s *Semaphore) TryAcquire(cost int64) bool {
	if cost <= 0 || cost > s.cfg.capacity {
		panic("semaphore: invalid cost")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used+cost <= s.cfg.capacity {
		s.used += cost
		s.inFlight.Add(cost) // under lock to keep inFlight consistent with used
		return true
	}
	return false
}

// Release 释放 cost 个单位的容量。
func (s *Semaphore) Release(cost int64) {
	s.mu.Lock()
	s.used -= cost
	if s.used < 0 {
		s.mu.Unlock()
		panic("semaphore: released more than acquired")
	}
	s.inFlight.Add(-cost)
	s.mu.Unlock()
	s.cond.Broadcast()
}

// Do 等权便捷方法:获取 1 个单位 → 执行 fn → 自动释放。等价于 bulkhead 模式。
func (s *Semaphore) Do(ctx context.Context, fn func() error) error {
	return s.DoWithCost(ctx, 1, fn)
}

// DoWithCost 加权便捷方法:获取 cost 个单位 → 执行 fn → 自动释放。
func (s *Semaphore) DoWithCost(ctx context.Context, cost int64, fn func() error) error {
	if err := s.Acquire(ctx, cost); err != nil {
		return err
	}
	defer s.Release(cost)
	return fn()
}

// InFlight 返回当前占用的容量。
func (s *Semaphore) InFlight() int64 {
	return s.inFlight.Load()
}

// Available 返回当前可用容量。
func (s *Semaphore) Available() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.capacity - s.used
}

// Capacity 返回总容量。
func (s *Semaphore) Capacity() int64 {
	return s.cfg.capacity
}

func (s *Semaphore) acquireWait(ctx context.Context, cost int64) error {
	deadline := time.Now().Add(s.cfg.maxWait)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		s.mu.Lock()
		if s.used+cost <= s.cfg.capacity {
			s.used += cost
			s.inFlight.Add(cost)
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			if s.cfg.onReject != nil {
				s.cfg.onReject()
			}
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				if s.cfg.onReject != nil {
					s.cfg.onReject()
				}
				return ErrFull
			}
		}
	}
}
