// Package eventbus 提供进程内、按主题(topic)分发的泛型事件总线:发布者按 topic
// 发事件,订阅者按 topic 注册回调处理函数,解耦"谁发"与"谁收"。
//
// 与 pkg/stream 的区别(互补):
//   - stream.Broadcaster 是单一数据源的 channel 扇出——所有订阅者收到同一份流,
//     订阅者用 range channel 消费,适合 SSE/WebSocket"一份数据推给 N 个连接";
//   - eventbus 是多主题、回调式——订阅者只收自己关心的 topic,处理逻辑写在回调里,
//     适合"模块间事件解耦"(presence 上线事件 → 通知模块 + 审计模块各自订阅)。
//
// 泛型 T 为事件负载类型。分发模式:
//   - 同步(默认):Publish 在调用方 goroutine 里依次执行所有 handler,返回即处理完;
//   - 异步(WithAsync):每个 handler 在独立 goroutine 执行(pkg/safe 恢复 panic),
//     Publish 不阻塞。同步模式下 handler 的 panic 也被恢复,不影响其它 handler。
//
// 并发安全。零值不可用,用 New 构造。
package eventbus

import (
	"sync"

	"github.com/rushteam/beauty/pkg/safe"
)

// Handler 事件处理回调。topic 为事件主题,payload 为负载。
type Handler[T any] func(topic string, payload T)

// config 配置。
type config struct {
	async   bool
	onPanic func(topic string, err error)
}

// Option 配置 Bus。
type Option func(*config)

// WithAsync 设置异步分发:每个 handler 在独立 goroutine 执行,Publish 不阻塞(默认同步)。
func WithAsync(async bool) Option { return func(c *config) { c.async = async } }

// WithOnPanic 设置 handler panic 时的回调(默认由 pkg/safe 静默恢复)。
func WithOnPanic(fn func(topic string, err error)) Option {
	return func(c *config) { c.onPanic = fn }
}

// subscription 一个订阅(handler + 唯一句柄,用于退订)。
type subscription[T any] struct {
	id      uint64
	handler Handler[T]
}

// Bus 按主题分发的事件总线。零值不可用,用 New 构造。并发安全。
type Bus[T any] struct {
	cfg config

	mu     sync.RWMutex
	topics map[string][]*subscription[T]
	nextID uint64
}

// New 创建事件总线。
func New[T any](opts ...Option) *Bus[T] {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	return &Bus[T]{cfg: cfg, topics: make(map[string][]*subscription[T])}
}

// Subscribe 订阅 topic,返回退订函数。多次订阅同一 topic 各自独立、都会被调用。
// 调用退订函数后该 handler 不再收到事件(幂等)。
func (b *Bus[T]) Subscribe(topic string, handler Handler[T]) (unsubscribe func()) {
	b.mu.Lock()
	b.nextID++
	sub := &subscription[T]{id: b.nextID, handler: handler}
	b.topics[topic] = append(b.topics[topic], sub)
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { b.remove(topic, sub.id) })
	}
}

func (b *Bus[T]) remove(topic string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.topics[topic]
	for i, s := range subs {
		if s.id == id {
			b.topics[topic] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(b.topics[topic]) == 0 {
		delete(b.topics, topic)
	}
}

// Publish 向 topic 发布一个事件,调用该 topic 下所有 handler。
// 返回被通知的 handler 数。同步模式下 Publish 返回时所有 handler 已执行完毕;
// 异步模式下仅表示已派发。
func (b *Bus[T]) Publish(topic string, payload T) int {
	b.mu.RLock()
	subs := b.topics[topic]
	// 拷贝一份 handler 快照,避免持锁执行回调(回调可能再订阅/退订/发布导致死锁)。
	handlers := make([]Handler[T], len(subs))
	for i, s := range subs {
		handlers[i] = s.handler
	}
	b.mu.RUnlock()

	for _, h := range handlers {
		b.dispatch(topic, h, payload)
	}
	return len(handlers)
}

func (b *Bus[T]) dispatch(topic string, h Handler[T], payload T) {
	if b.cfg.async {
		safe.Go(func() { h(topic, payload) }, func(err error) {
			if b.cfg.onPanic != nil {
				b.cfg.onPanic(topic, err)
			}
		})
		return
	}
	// 同步:panic 恢复(有回调则上报),不影响其它 handler 与调用方。
	if err := safe.Run(func() error { h(topic, payload); return nil }); err != nil && b.cfg.onPanic != nil {
		b.cfg.onPanic(topic, err)
	}
}

// SubscriberCount 返回某 topic 当前的订阅者数。
func (b *Bus[T]) SubscriberCount(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.topics[topic])
}

// Topics 返回当前有订阅者的所有 topic(顺序不保证)。
func (b *Bus[T]) Topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.topics))
	for t := range b.topics {
		out = append(out, t)
	}
	return out
}
