package stream

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

// Codec 在 T 与 mq 消息体之间编解码（序列化是 policy，由调用方选择）。
type Codec[T any] interface {
	Marshal(v T) ([]byte, error)
	Unmarshal(data []byte) (T, error)
}

// JSON 返回基于 encoding/json 的 Codec。
func JSON[T any]() Codec[T] { return jsonCodec[T]{} }

type jsonCodec[T any] struct{}

func (jsonCodec[T]) Marshal(v T) ([]byte, error) { return json.Marshal(v) }

func (jsonCodec[T]) Unmarshal(data []byte) (T, error) {
	var v T
	err := json.Unmarshal(data, &v)
	return v, err
}

// Raw 返回不做转换的 Codec，适合已编码好的负载（如预渲染的 SSE 帧）。
// 解码出的切片可能被同一节点的所有订阅者共享，订阅者不应修改它。
func Raw() Codec[[]byte] { return rawCodec{} }

type rawCodec struct{}

func (rawCodec) Marshal(v []byte) ([]byte, error)      { return v, nil }
func (rawCodec) Unmarshal(data []byte) ([]byte, error) { return data, nil }

// TopicPrefix 返回 "prefix + key" 形式的 topic 映射，key 用 fmt.Sprint 格式化。
func TopicPrefix[K comparable](prefix string) func(K) string {
	return func(k K) string { return prefix + fmt.Sprint(k) }
}

// Bridge 是跨节点的 Hub：本地按 key 扇出（同 Hub），消息总线（mq）做 backplane。
//
//   - Publish 只发到 mq，不直接投本地；所有节点（含本节点）都经 mq 回调收到后再投给本地订阅者，
//     同一 key 的投递顺序由 broker 决定（Redis Pub/Sub 下各节点一致），本节点不会先于其它节点看到。
//   - 每个 key 在本节点的首个订阅者到来时向 mq 订阅 topic(key)，最后一个离开时退订：
//     节点只接收自己有人在听的 key，空闲 key 不占 broker 资源。
//   - mq 订阅不设 group（扇出），投递保证继承所用 broker；用 Redis Pub/Sub、NATS core 等
//     即 at-most-once 广播。慢订阅者按 DropMode 丢弃，不阻塞 mq 投递。
//
// 应用侧只需提供 topic 映射与 Codec。零值不可用，请用 NewBridge 构造。Bridge 并发安全。
type Bridge[K comparable, T any] struct {
	pub   mq.Publisher
	sub   mq.Subscriber
	topic func(K) string
	codec Codec[T]
	reg   *registry[K, T]

	ctx    context.Context
	cancel context.CancelFunc
}

// NewBridge 创建 Bridge。pub / sub 通常是同一个 broker（如 redis.PubSub）；
// topic 把 key 映射为 mq topic（可用 TopicPrefix）；opts 作用于每个 key 的本地 Broadcaster。
func NewBridge[K comparable, T any](pub mq.Publisher, sub mq.Subscriber, topic func(K) string, codec Codec[T], opts ...Option) *Bridge[K, T] {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bridge[K, T]{pub: pub, sub: sub, topic: topic, codec: codec, ctx: ctx, cancel: cancel}
	b.reg = newRegistry[K, T](b.activate, opts)
	return b
}

func (b *Bridge[K, T]) activate(key K, e *entry[T]) (func(), error) {
	subCtx, cancel := context.WithCancel(b.ctx)
	topic := b.topic(key)
	// 回调直接投递到本次激活的 Broadcaster 而不是按 key 查找：key 回收后马上重建时，
	// 旧订阅尚未退订完的残余消息只会落到已关闭的旧 Broadcaster 上，不会重复投递。
	err := b.sub.Subscribe(subCtx, topic, func(_ context.Context, msg mq.Message) error {
		v, err := b.codec.Unmarshal(msg.Body)
		if err != nil {
			return fmt.Errorf("stream: decode message on %q: %w", msg.Topic, err)
		}
		e.b.Publish(v)
		return nil
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stream: subscribe %q: %w", topic, err)
	}
	return cancel, nil
}

// Subscribe 订阅 key。本节点该 key 的首个订阅者会先完成 mq 订阅再返回，
// 返回即表示之后发布到该 key 的消息可达；mq 订阅失败时返回 error。
// 取消函数（或 ctx 取消）注销订阅并关闭 channel，本节点最后一个订阅者离开时退订 mq。
func (b *Bridge[K, T]) Subscribe(ctx context.Context, key K) (<-chan T, func(), error) {
	return b.reg.subscribe(ctx, key)
}

// Publish 把 v 编码后发布到 topic(key)，经 mq 广播给所有节点上该 key 的订阅者。
func (b *Bridge[K, T]) Publish(ctx context.Context, key K, v T) error {
	if b.ctx.Err() != nil {
		return ErrClosed
	}
	body, err := b.codec.Marshal(v)
	if err != nil {
		return fmt.Errorf("stream: encode: %w", err)
	}
	return b.pub.Publish(ctx, mq.Message{Topic: b.topic(key), Body: body})
}

// SubscriberCount 返回本节点 key 的订阅者数。
func (b *Bridge[K, T]) SubscriberCount(key K) int { return b.reg.subscriberCount(key) }

// Len 返回本节点当前有订阅者的 key 数（即持有的 mq 订阅数）。
func (b *Bridge[K, T]) Len() int { return b.reg.len() }

// Close 退订全部 mq 订阅并关闭本地订阅者 channel，之后 Subscribe / Publish 返回 ErrClosed。
// 不关闭 pub / sub，它们由调用方管理。可重复调用。
func (b *Bridge[K, T]) Close() {
	b.cancel()
	b.reg.close()
}
