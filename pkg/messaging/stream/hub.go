package stream

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed 表示 Hub / Bridge 已关闭。
var ErrClosed = errors.New("stream: closed")

// Hub 是按 key 分组的 Broadcaster 注册表：每个 key（房间、频道、用户……）一个独立的
// Broadcaster，首个订阅者到来时懒建，最后一个订阅者离开时自动回收。语义与 Broadcaster
// 逐条对应，只是多了 key 维度；不含任何领域信息。
//
// 零值不可用，请用 NewHub 构造。Hub 并发安全。
type Hub[K comparable, T any] struct {
	reg *registry[K, T]
}

// NewHub 创建 Hub。opts 作用于每个 key 的 Broadcaster（缓冲大小、丢弃策略）。
func NewHub[K comparable, T any](opts ...Option) *Hub[K, T] {
	return &Hub[K, T]{reg: newRegistry[K, T](nil, opts)}
}

// Subscribe 订阅 key，返回只读 channel 与取消函数；该 key 尚无 Broadcaster 时创建。
// 取消函数（或 ctx 取消）注销订阅并关闭 channel，key 的最后一个订阅者离开时回收其 Broadcaster。
// Hub 已 Close 时返回已关闭的 channel 和空操作的取消函数。
func (h *Hub[K, T]) Subscribe(ctx context.Context, key K) (<-chan T, func()) {
	ch, cancel, err := h.reg.subscribe(ctx, key)
	if err != nil {
		c := make(chan T)
		close(c)
		return c, func() {}
	}
	return ch, cancel
}

// Publish 把 v 广播给 key 的当前订阅者（非阻塞，满队列按 DropMode 丢弃）。
// key 没有订阅者时直接丢弃、不创建 Broadcaster。返回成功投递的订阅者数。
func (h *Hub[K, T]) Publish(key K, v T) int { return h.reg.publish(key, v) }

// SubscriberCount 返回 key 的当前订阅者数。
func (h *Hub[K, T]) SubscriberCount(key K) int { return h.reg.subscriberCount(key) }

// Len 返回当前有订阅者的 key 数。
func (h *Hub[K, T]) Len() int { return h.reg.len() }

// Close 关闭全部 key 的 Broadcaster（订阅者 channel 随之关闭），之后 Subscribe 返回已关闭的
// channel、Publish 为 no-op。可重复调用。
func (h *Hub[K, T]) Close() { h.reg.close() }

// registry 是 Hub 与 Bridge 共用的按 key 引用计数注册表。
// activate 在 key 的首个订阅者到来时调用（锁外执行，可做网络操作），返回的 deactivate 在
// 最后一个订阅者离开时调用；为 nil 表示纯本地。激活期间到来的同 key 订阅者等待同一次激活结果。
type registry[K comparable, T any] struct {
	activate func(key K, e *entry[T]) (deactivate func(), err error)
	opts     []Option

	mu      sync.Mutex
	entries map[K]*entry[T]
	closed  bool
}

type entry[T any] struct {
	b    *Broadcaster[T]
	refs int // 已登记的订阅者数（含仍在等待激活的）

	ready      chan struct{} // 激活完成后关闭；之后 err / deactivate 只读
	err        error
	deactivate func()
}

func newRegistry[K comparable, T any](activate func(K, *entry[T]) (func(), error), opts []Option) *registry[K, T] {
	return &registry[K, T]{activate: activate, opts: opts, entries: make(map[K]*entry[T])}
}

func (r *registry[K, T]) subscribe(ctx context.Context, key K) (<-chan T, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, nil, ErrClosed
	}
	e, ok := r.entries[key]
	if !ok {
		e = &entry[T]{b: New[T](r.opts...), ready: make(chan struct{})}
		r.entries[key] = e
	}
	e.refs++
	r.mu.Unlock()

	if !ok {
		if r.activate != nil {
			e.deactivate, e.err = r.activate(key, e)
		}
		if e.err != nil {
			// 失败的条目立即摘掉，后来者重新激活而不是复用这次失败
			r.mu.Lock()
			if r.entries[key] == e {
				delete(r.entries, key)
			}
			r.mu.Unlock()
		}
		close(e.ready)
	} else {
		select {
		case <-e.ready:
		case <-ctx.Done():
			r.release(key, e)
			return nil, nil, ctx.Err()
		}
	}
	if e.err != nil {
		r.release(key, e)
		return nil, nil, e.err
	}

	// ctx 由下面的 goroutine 统一处理：取消时既要退订 Broadcaster，也要释放引用计数
	ch, bcancel := e.b.Subscribe(context.Background())
	done := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			close(done)
			bcancel()
			r.release(key, e)
		})
	}
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-done:
			}
		}()
	}
	return ch, cancel, nil
}

func (r *registry[K, T]) release(key K, e *entry[T]) {
	r.mu.Lock()
	e.refs--
	last := e.refs == 0 && r.entries[key] == e
	if last {
		delete(r.entries, key)
	}
	r.mu.Unlock()
	if last {
		e.b.Close()
		if e.deactivate != nil {
			e.deactivate()
		}
	}
}

func (r *registry[K, T]) publish(key K, v T) int {
	r.mu.Lock()
	e := r.entries[key]
	r.mu.Unlock()
	if e == nil {
		return 0
	}
	return e.b.Publish(v)
}

func (r *registry[K, T]) subscriberCount(key K) int {
	r.mu.Lock()
	e := r.entries[key]
	r.mu.Unlock()
	if e == nil {
		return 0
	}
	return e.b.SubscriberCount()
}

func (r *registry[K, T]) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *registry[K, T]) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	entries := r.entries
	r.entries = make(map[K]*entry[T])
	r.mu.Unlock()
	for _, e := range entries {
		e.b.Close()
		<-e.ready
		if e.deactivate != nil {
			e.deactivate()
		}
	}
}
