// Package kafka 是 pkg/mq 的 Kafka broker 绑定,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/kafka),不进 beauty 核心依赖图。基于
// segmentio/kafka-go 实现 mq.Publisher / mq.Subscriber。
//
// 语义映射:
//   - topic → Kafka topic;mq.Message.Key → Kafka 消息 Key(决定分区、保序);Headers → Kafka Headers;
//   - mq.WithGroup(g) → Kafka consumer group(同组按分区竞争消费)。Kafka 消费天生基于 consumer
//     group,因此 Subscribe **必须**指定 group(WithGroup 或 SubscriberOption 默认组);
//     要"扇出"(每个实例都收到全部)给每个实例配**不同** group 即可。
//
// 投递保证:at-least-once——handler 成功后才提交 offset(处理失败不提交,下次重投)。
// 故 handler 应幂等(可配 pkg/mq 的 idempotency / Retry 中间件)。订阅随 ctx 取消停止。
//
// 注意:端到端需要真实 Kafka broker;本模块单测只覆盖消息映射与构造,broker 互操作请在
// 具备 Kafka 的环境验证(与 etcd/gohlslib 等外部依赖同一约定)。
package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/rushteam/beauty/pkg/mq"
)

// Publisher 实现 mq.Publisher(基于 kafka.Writer,内部按 topic 路由,连接复用)。
type Publisher struct {
	w *kafka.Writer
}

var _ mq.Publisher = (*Publisher)(nil)

// PublisherOption 配置 Publisher。
type PublisherOption func(*kafka.Writer)

// WithBalancer 设置分区均衡策略(默认 kafka-go 的 LeastBytes;要按 Key 保序用 &kafka.Hash{})。
func WithBalancer(b kafka.Balancer) PublisherOption {
	return func(w *kafka.Writer) { w.Balancer = b }
}

// WithWriterTimeout 设置写超时。
func WithWriterTimeout(d time.Duration) PublisherOption {
	return func(w *kafka.Writer) { w.WriteTimeout = d }
}

// NewPublisher 创建发布者。brokers 是 bootstrap 地址(如 []string{"127.0.0.1:9092"})。
func NewPublisher(brokers []string, opts ...PublisherOption) *Publisher {
	w := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Balancer:               &kafka.Hash{}, // 按 Key 哈希分区,保证同 Key 有序
		AllowAutoTopicCreation: false,
	}
	for _, o := range opts {
		o(w)
	}
	return &Publisher{w: w}
}

// Publish 实现 mq.Publisher。
func (p *Publisher) Publish(ctx context.Context, msg mq.Message) error {
	if err := p.w.WriteMessages(ctx, toKafka(msg)); err != nil {
		return fmt.Errorf("kafka: write %s: %w", msg.Topic, err)
	}
	return nil
}

// Close 关闭 writer。
func (p *Publisher) Close() error { return p.w.Close() }

// Subscriber 实现 mq.Subscriber。每个 Subscribe 起一个 consumer group reader。
type Subscriber struct {
	brokers    []string
	minBytes   int
	maxBytes   int
	startFirst bool // 无提交位点时从最早开始(默认从最新)
}

var _ mq.Subscriber = (*Subscriber)(nil)

// SubscriberOption 配置 Subscriber。
type SubscriberOption func(*Subscriber)

// WithFetchBounds 设置单次拉取字节范围(默认 1B~1MB)。
func WithFetchBounds(minBytes, maxBytes int) SubscriberOption {
	return func(s *Subscriber) { s.minBytes, s.maxBytes = minBytes, maxBytes }
}

// WithStartFromFirst 无已提交位点时从最早消息开始消费(默认从最新)。
func WithStartFromFirst() SubscriberOption {
	return func(s *Subscriber) { s.startFirst = true }
}

// NewSubscriber 创建订阅者。
func NewSubscriber(brokers []string, opts ...SubscriberOption) *Subscriber {
	s := &Subscriber{brokers: brokers, minBytes: 1, maxBytes: 1 << 20}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ErrGroupRequired 表示未提供 consumer group。Kafka 消费必须指定 group(见包注释)。
var ErrGroupRequired = errors.New("kafka: subscribe requires a consumer group (use mq.WithGroup)")

// Subscribe 实现 mq.Subscriber:为 topic 起一个 consumer group reader,at-least-once
// (handler 成功后提交 offset),ctx 取消即停。必须经 mq.WithGroup 指定 group。
func (s *Subscriber) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	cfg := mq.ApplySubOptions(opts...)
	if cfg.Group == "" {
		return ErrGroupRequired
	}
	startOffset := kafka.LastOffset
	if s.startFirst {
		startOffset = kafka.FirstOffset
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     s.brokers,
		Topic:       topic,
		GroupID:     cfg.Group,
		MinBytes:    s.minBytes,
		MaxBytes:    s.maxBytes,
		StartOffset: startOffset,
	})
	go func() {
		defer r.Close()
		for {
			m, err := r.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() == nil {
					slog.Debug("kafka: fetch", "topic", topic, "group", cfg.Group, "err", err)
				}
				return // ctx 取消或读失败,退出
			}
			if herr := h(ctx, fromKafka(m)); herr != nil {
				// 处理失败:不提交 offset,下次重投(at-least-once)。
				if ctx.Err() == nil {
					slog.Debug("kafka: handler error, not committing", "topic", topic, "err", herr)
				}
				continue
			}
			if err := r.CommitMessages(ctx, m); err != nil && ctx.Err() == nil {
				slog.Debug("kafka: commit", "topic", topic, "err", err)
			}
		}
	}()
	return nil
}

// ===== 消息映射(纯函数,可单测)=====

func toKafka(msg mq.Message) kafka.Message {
	km := kafka.Message{Topic: msg.Topic, Key: []byte(msg.Key), Value: msg.Body}
	if len(msg.Headers) > 0 {
		km.Headers = make([]kafka.Header, 0, len(msg.Headers))
		for k, v := range msg.Headers {
			km.Headers = append(km.Headers, kafka.Header{Key: k, Value: []byte(v)})
		}
	}
	return km
}

func fromKafka(km kafka.Message) mq.Message {
	msg := mq.Message{Topic: km.Topic, Key: string(km.Key), Body: km.Value}
	if len(km.Headers) > 0 {
		msg.Headers = make(map[string]string, len(km.Headers))
		for _, hh := range km.Headers {
			msg.Headers[hh.Key] = string(hh.Value)
		}
	}
	return msg
}
