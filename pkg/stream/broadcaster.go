// Package stream 提供流式扇出（广播）原语：把一个事件源 fan-out 给多个订阅者，
// 适合 SSE / WebSocket 等"一份数据推给 N 个连接"的场景。
package stream

import (
	"context"
	"sync"
)

// Broadcaster 把 Publish 的值广播给当前所有订阅者。
// 每个订阅者有独立的带缓冲队列；当某订阅者消费过慢、队列写满时，
// 按策略丢弃（默认丢最旧），保证慢订阅者不拖垮发布端与其它订阅者。
//
// 零值不可用，请用 New 构造。Broadcaster 并发安全。
type Broadcaster[T any] struct {
	mu       sync.RWMutex
	subs     map[*subscriber[T]]struct{}
	bufSize  int
	dropMode DropMode
	closed   bool
}

// DropMode 决定订阅者队列写满时的丢弃策略。
type DropMode int

const (
	// DropOldest 丢弃队列中最旧的一条，写入新的（默认，适合"只关心最新"的推送）。
	DropOldest DropMode = iota
	// DropNewest 丢弃当前这条新值，保留队列已有内容。
	DropNewest
)

// Option 配置 Broadcaster。
type Option func(*config)

type config struct {
	bufSize  int
	dropMode DropMode
}

// WithBufferSize 设置每个订阅者的队列容量，默认 16。
func WithBufferSize(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.bufSize = n
		}
	}
}

// WithDropMode 设置队列写满时的丢弃策略，默认 DropOldest。
func WithDropMode(m DropMode) Option {
	return func(c *config) { c.dropMode = m }
}

// New 创建一个 Broadcaster。
func New[T any](opts ...Option) *Broadcaster[T] {
	cfg := config{bufSize: 16, dropMode: DropOldest}
	for _, o := range opts {
		o(&cfg)
	}
	return &Broadcaster[T]{
		subs:     make(map[*subscriber[T]]struct{}),
		bufSize:  cfg.bufSize,
		dropMode: cfg.dropMode,
	}
}

type subscriber[T any] struct {
	ch     chan T
	once   sync.Once
	closed chan struct{}
}

// Subscribe 注册一个订阅者，返回只读 channel 与取消函数。
// 取消函数（或 ctx 取消）会注销订阅并关闭返回的 channel。
// Broadcaster 已 Close 时，返回一个已关闭的 channel 和空操作的取消函数。
func (b *Broadcaster[T]) Subscribe(ctx context.Context) (<-chan T, func()) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		ch := make(chan T)
		close(ch)
		return ch, func() {}
	}
	s := &subscriber[T]{
		ch:     make(chan T, b.bufSize),
		closed: make(chan struct{}),
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	cancel := func() { b.unsubscribe(s) }

	// ctx 取消时自动注销
	if ctx != nil && ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-s.closed:
			}
		}()
	}
	return s.ch, cancel
}

func (b *Broadcaster[T]) unsubscribe(s *subscriber[T]) {
	b.mu.Lock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		s.once.Do(func() {
			close(s.closed)
			close(s.ch)
		})
	}
	b.mu.Unlock()
}

// Publish 把 v 广播给当前所有订阅者（非阻塞）。
// 某订阅者队列满时按 DropMode 丢弃，不会阻塞发布端。
// 返回成功投递（含因丢旧而入队）的订阅者数。
func (b *Broadcaster[T]) Publish(v T) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return 0
	}
	delivered := 0
	for s := range b.subs {
		if b.send(s, v) {
			delivered++
		}
	}
	return delivered
}

func (b *Broadcaster[T]) send(s *subscriber[T], v T) bool {
	select {
	case s.ch <- v:
		return true
	default:
	}
	// 队列已满
	if b.dropMode == DropNewest {
		return false
	}
	// DropOldest：丢一条最旧的，再尝试写入
	select {
	case <-s.ch:
	default:
	}
	select {
	case s.ch <- v:
		return true
	default:
		return false
	}
}

// SubscriberCount 返回当前订阅者数量。
func (b *Broadcaster[T]) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Close 关闭广播器：注销并关闭所有订阅者的 channel，之后 Publish 为 no-op、
// Subscribe 返回已关闭的 channel。可重复调用。
func (b *Broadcaster[T]) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		s.once.Do(func() {
			close(s.closed)
			close(s.ch)
		})
		delete(b.subs, s)
	}
}
