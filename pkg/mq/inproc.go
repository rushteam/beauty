package mq

import (
	"context"
	"log/slog"
	"sync"
)

// InProc 是零依赖的进程内 broker,同时实现 Publisher 与 Subscriber——用于单体部署、开发
// 与测试。语义对齐真 broker:同 topic 下不设 group 的订阅者**扇出**(都收到),同一
// (topic, group) 的订阅者**竞争消费**(轮询,每条只投一个)。投递经每订阅一条缓冲 channel +
// 一个投递 goroutine,不阻塞发布(缓冲满则背压)。at-most-once:handler 出错只记日志、不重投
// (用 Retry 中间件兜瞬时错误)。
type InProc struct {
	defaultBuffer int

	mu     sync.Mutex
	subs   map[string][]*subscription // topic → 订阅
	rr     map[string]int             // (topic|group) → 轮询计数
	closed bool
}

type subscription struct {
	topic string
	group string
	h     Handler
	ch    chan Message
}

// InProcOption 配置 InProc。
type InProcOption func(*InProc)

// WithDefaultBuffer 设置订阅默认投递缓冲(未用 WithBuffer 指定时;默认 64)。
func WithDefaultBuffer(n int) InProcOption {
	return func(b *InProc) {
		if n > 0 {
			b.defaultBuffer = n
		}
	}
}

// NewInProc 创建进程内 broker。
func NewInProc(opts ...InProcOption) *InProc {
	b := &InProc{
		defaultBuffer: 64,
		subs:          make(map[string][]*subscription),
		rr:            make(map[string]int),
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

var (
	_ Publisher  = (*InProc)(nil)
	_ Subscriber = (*InProc)(nil)
)

// Subscribe 注册订阅;ctx 取消即解除该订阅并停止其投递 goroutine。
func (b *InProc) Subscribe(ctx context.Context, topic string, h Handler, opts ...SubscribeOption) error {
	cfg := ApplySubOptions(opts...)
	buf := cfg.Buffer
	if buf <= 0 {
		buf = b.defaultBuffer
	}
	sub := &subscription{topic: topic, group: cfg.Group, h: h, ch: make(chan Message, buf)}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	b.subs[topic] = append(b.subs[topic], sub)
	b.mu.Unlock()

	go b.deliver(ctx, sub) // 投递 goroutine
	go func() {            // ctx 取消 → 解除订阅
		<-ctx.Done()
		b.unsubscribe(sub)
	}()
	return nil
}

func (b *InProc) deliver(ctx context.Context, sub *subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-sub.ch:
			if err := sub.h(ctx, msg); err != nil && ctx.Err() == nil {
				slog.Debug("mq: handler error", "topic", sub.topic, "group", sub.group, "err", err)
			}
		}
	}
}

func (b *InProc) unsubscribe(target *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subs[target.topic]
	for i, s := range subs {
		if s == target {
			b.subs[target.topic] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(b.subs[target.topic]) == 0 {
		delete(b.subs, target.topic)
	}
}

// Publish 把消息投给 topic 的订阅者:非 group 订阅者各投一份(扇出),每个 group 轮询选一个投递。
func (b *InProc) Publish(ctx context.Context, msg Message) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	// 快照本 topic 的投递目标(在锁内决定,锁外发送以免长时间持锁)。
	subs := b.subs[msg.Topic]
	targets := make([]*subscription, 0, len(subs))
	groups := make(map[string][]*subscription)
	for _, s := range subs {
		if s.group == "" {
			targets = append(targets, s) // 扇出
		} else {
			groups[s.group] = append(groups[s.group], s)
		}
	}
	for g, members := range groups { // 每组轮询选一个
		key := msg.Topic + "|" + g
		idx := b.rr[key] % len(members)
		b.rr[key] = (b.rr[key] + 1) % (1 << 30)
		targets = append(targets, members[idx])
	}
	b.mu.Unlock()

	for _, s := range targets {
		select {
		case s.ch <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Close 关闭 broker:拒绝后续发布/订阅。已存在的订阅由各自 ctx 解除。幂等。
func (b *InProc) Close() error {
	b.mu.Lock()
	b.closed = true
	b.subs = make(map[string][]*subscription)
	b.mu.Unlock()
	return nil
}
