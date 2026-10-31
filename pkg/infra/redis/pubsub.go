package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

// ErrGroupNotSupported 表示 Pub/Sub 不支持 mq.WithGroup（没有消费组概念）。
var ErrGroupNotSupported = errors.New("redis pubsub: consumer group not supported, use contrib/redisstream")

// PubSub 用 Redis / Valkey 的 Pub/Sub（PUBLISH / SUBSCRIBE）实现 mq.Publisher 与 mq.Subscriber，
// 适合做广播 backplane（如 pkg/messaging/stream.Bridge）。只用 RESP 基础命令，Valkey 同样适用。
//
// 语义（如实标注）：
//   - 扇出、at-most-once：不持久化、不重投，连接断开期间的消息丢失。需要持久化 / 重投 /
//     消费组用 contrib/redisstream（Redis Streams）。
//   - 不支持 mq.WithGroup：传入时返回 ErrGroupNotSupported，而不是静默退化为扇出。
//   - 所有订阅复用一条专用连接（go-redis PubSub，断线自动重连并重新订阅）；同一 topic 的多次
//     Subscribe 共享一次 SUBSCRIBE，最后一个退订时 UNSUBSCRIBE。
//   - Subscribe 等到服务端确认 SUBSCRIBE 才返回：返回之后发布的消息保证可达。
//   - 每个订阅有独立投递缓冲与 goroutine；缓冲满时丢弃该订阅的新消息（计入 Dropped），
//     不阻塞同一连接上其它 topic 的投递。
//   - Key / Headers 用紧凑二进制信封随消息传递；两者都为空时 payload 即 Body 原文，
//     可与 redis-cli 等非 beauty 客户端互通。
//   - 传入 ClusterClient 时使用普通 Pub/Sub（消息在集群内广播）。
//
// 零值不可用，用 NewPubSub / NewPubSubFromConfig 构造。
type PubSub struct {
	client     redis.UniversalClient
	ownsClient bool
	buffer     int
	subTimeout time.Duration

	mu     sync.Mutex
	ps     *redis.PubSub // 首次 Subscribe 时建立
	topics map[string]*topicState
	closed bool

	done     chan struct{}
	dispatch sync.WaitGroup
	dropped  atomic.Uint64
}

var (
	_ mq.Publisher  = (*PubSub)(nil)
	_ mq.Subscriber = (*PubSub)(nil)
)

// topicState 记录一个 channel 的本地订阅与 SUBSCRIBE 确认进度。
// sent / acked 用于判断本次 SUBSCRIBE 是否已被服务端确认：退订后立即重订时，旧命令迟到的
// 确认只会推进 acked 到旧值，不会让新的订阅者提前返回。
type topicState struct {
	subs    map[*pubsubSub]struct{}
	sent    uint64
	acked   uint64
	waiters []ackWaiter
}

type ackWaiter struct {
	target uint64
	ch     chan struct{}
}

type pubsubSub struct {
	topic string
	h     mq.Handler
	ch    chan mq.Message
	stop  chan struct{}
	once  sync.Once
}

// PubSubOption 配置 PubSub。
type PubSubOption func(*PubSub)

// WithPubSubBuffer 设置每个订阅的默认投递缓冲（未用 mq.WithBuffer 指定时；默认 256）。
func WithPubSubBuffer(n int) PubSubOption {
	return func(p *PubSub) {
		if n > 0 {
			p.buffer = n
		}
	}
}

// WithSubscribeTimeout 设置 Subscribe 等待服务端确认的上限（默认 5s）。
func WithSubscribeTimeout(d time.Duration) PubSubOption {
	return func(p *PubSub) {
		if d > 0 {
			p.subTimeout = d
		}
	}
}

// NewPubSub 用已有客户端创建 PubSub。client 由调用方管理生命周期，Close 不关闭它。
func NewPubSub(client redis.UniversalClient, opts ...PubSubOption) *PubSub {
	p := &PubSub{
		client:     client,
		buffer:     256,
		subTimeout: 5 * time.Second,
		topics:     make(map[string]*topicState),
		done:       make(chan struct{}),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// NewPubSubFromConfig 复用 Config 建连接（并 Ping 校验）后创建 PubSub；Close 时一并关闭该连接。
func NewPubSubFromConfig(c *Config, opts ...PubSubOption) (*PubSub, error) {
	client, err := pingClient(c)
	if err != nil {
		return nil, fmt.Errorf("redis pubsub: %w", err)
	}
	p := NewPubSub(client, opts...)
	p.ownsClient = true
	return p, nil
}

// Publish 发布一条消息（PUBLISH）。没有订阅者时消息直接丢弃，这是 Pub/Sub 的语义。
func (p *PubSub) Publish(ctx context.Context, msg mq.Message) error {
	select {
	case <-p.done:
		return mq.ErrClosed
	default:
	}
	return p.client.Publish(ctx, msg.Topic, encodeMessage(msg)).Err()
}

// Subscribe 订阅 topic，等到服务端确认后返回；ctx 取消即解除该订阅。
func (p *PubSub) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	cfg := mq.ApplySubOptions(opts...)
	if cfg.Group != "" {
		return ErrGroupNotSupported
	}
	buf := cfg.Buffer
	if buf <= 0 {
		buf = p.buffer
	}
	s := &pubsubSub{topic: topic, h: h, ch: make(chan mq.Message, buf), stop: make(chan struct{})}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return mq.ErrClosed
	}
	p.ensureConnLocked()
	st := p.topics[topic]
	if st == nil {
		st = &topicState{subs: make(map[*pubsubSub]struct{})}
		p.topics[topic] = st
	}
	st.subs[s] = struct{}{}
	if len(st.subs) == 1 {
		// 在锁内发 SUBSCRIBE / UNSUBSCRIBE，保证命令顺序与本地订阅状态一致
		wctx, cancel := context.WithTimeout(ctx, p.subTimeout)
		err := p.ps.Subscribe(wctx, topic)
		cancel()
		if err != nil {
			delete(st.subs, s)
			p.unsubscribeIfIdleLocked(topic, st)
			p.mu.Unlock()
			return fmt.Errorf("redis pubsub: subscribe %q: %w", topic, err)
		}
		st.sent++
	}
	var wait chan struct{}
	if st.acked < st.sent {
		wait = make(chan struct{})
		st.waiters = append(st.waiters, ackWaiter{target: st.sent, ch: wait})
	}
	p.mu.Unlock()

	if wait != nil {
		timer := time.NewTimer(p.subTimeout)
		defer timer.Stop()
		select {
		case <-wait:
		case <-ctx.Done():
			p.remove(s)
			return ctx.Err()
		case <-p.done:
			return mq.ErrClosed
		case <-timer.C:
			p.remove(s)
			return fmt.Errorf("redis pubsub: subscribe %q: no confirmation within %s", topic, p.subTimeout)
		}
	}

	go p.deliver(ctx, s)
	go func() {
		select {
		case <-ctx.Done():
		case <-p.done:
		case <-s.stop:
		}
		p.remove(s)
	}()
	return nil
}

// Dropped 返回因订阅缓冲已满而丢弃的消息总数。
func (p *PubSub) Dropped() uint64 { return p.dropped.Load() }

// Close 关闭订阅连接并解除全部订阅，之后 Publish / Subscribe 返回 mq.ErrClosed。幂等。
func (p *PubSub) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	ps := p.ps
	p.topics = make(map[string]*topicState)
	p.mu.Unlock()

	close(p.done)
	var err error
	if ps != nil {
		err = ps.Close()
		p.dispatch.Wait()
	}
	if p.ownsClient {
		err = errors.Join(err, p.client.Close())
	}
	return err
}

func (p *PubSub) ensureConnLocked() {
	if p.ps != nil {
		return
	}
	p.ps = p.client.Subscribe(context.Background())
	ch := p.ps.ChannelWithSubscriptions(redis.WithChannelSize(1024))
	p.dispatch.Add(1)
	go func() {
		defer p.dispatch.Done()
		for m := range ch {
			switch v := m.(type) {
			case *redis.Subscription:
				if v.Kind == "subscribe" {
					p.onSubscribed(v.Channel)
				}
			case *redis.Message:
				p.onMessage(v)
			}
		}
	}()
}

func (p *PubSub) onSubscribed(topic string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.topics[topic]
	if st == nil {
		return
	}
	// 断线重连后的重新订阅也会产生确认，它同样意味着服务端已订阅，但不能超过已发送数
	if st.acked < st.sent {
		st.acked++
	}
	kept := st.waiters[:0]
	for _, w := range st.waiters {
		if w.target <= st.acked {
			close(w.ch)
		} else {
			kept = append(kept, w)
		}
	}
	st.waiters = kept
	if len(st.subs) == 0 && st.acked >= st.sent {
		delete(p.topics, topic)
	}
}

func (p *PubSub) onMessage(v *redis.Message) {
	msg := decodeMessage(v.Channel, v.Payload)
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.topics[v.Channel]
	if st == nil {
		return
	}
	for s := range st.subs {
		select {
		case s.ch <- msg:
		default:
			p.dropped.Add(1)
		}
	}
}

func (p *PubSub) deliver(ctx context.Context, s *pubsubSub) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-p.done:
			return
		case msg := <-s.ch:
			if err := s.h(ctx, msg); err != nil && ctx.Err() == nil {
				slog.Debug("redis pubsub: handler error", "topic", s.topic, "err", err)
			}
		}
	}
}

func (p *PubSub) remove(s *pubsubSub) {
	p.mu.Lock()
	if st := p.topics[s.topic]; st != nil {
		if _, ok := st.subs[s]; ok {
			delete(st.subs, s)
			p.unsubscribeIfIdleLocked(s.topic, st)
		}
	}
	p.mu.Unlock()
	s.once.Do(func() { close(s.stop) })
}

// unsubscribeIfIdleLocked 在 topic 已无本地订阅时退订；仍有未确认的 SUBSCRIBE 时保留状态，
// 等确认到齐后由 onSubscribed 清理。
func (p *PubSub) unsubscribeIfIdleLocked(topic string, st *topicState) {
	if len(st.subs) > 0 {
		return
	}
	if !p.closed && p.ps != nil {
		// go-redis 会先把 channel 移出重订阅集合再发命令，写失败也不会在重连后被订阅回来
		if err := p.ps.Unsubscribe(context.Background(), topic); err != nil {
			slog.Debug("redis pubsub: unsubscribe failed", "topic", topic, "err", err)
		}
	}
	st.waiters = nil
	if st.acked >= st.sent {
		delete(p.topics, topic)
	}
}
