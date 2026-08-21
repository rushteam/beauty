// Package kafka 是 pkg/mq 的 Kafka broker 绑定,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/kafka),不进 beauty 核心依赖图。基于
// twmb/franz-go 实现 mq.Publisher / mq.Subscriber,并默认挂载官方 OTel 插件 kotel
// (publish / receive / process span + metrics)。
//
// 语义映射:
//   - topic → Kafka topic;mq.Message.Key → Kafka Key(决定分区、保序);Headers → Kafka Headers;
//   - mq.WithGroup(g) → Kafka consumer group(同组按分区竞争消费)。Kafka 消费天生基于 consumer
//     group,因此 Subscribe **必须**指定 group;要"扇出"给每个实例配**不同** group 即可。
//
// 投递保证:at-least-once——handler 成功后才 CommitRecords(处理失败不提交,下次重投/
// rebalance 后重投)。故 handler 应幂等。订阅随 ctx 取消停止。
//
// 消费起始位置:新 consumer group 首次消费时默认从**最早消息**开始(AtStart),
// 与 franz-go 及 Kafka auto.offset.reset=earliest 默认行为一致;
// 可用 WithStartFromEnd 改为从最新开始(忽略历史)。已有 committed offset 时
// 从上次提交的位置续读,不受此设置影响。
//
// OTel:默认启用 kotel(使用全局 TracerProvider/MeterProvider/Propagator;未配 beauty.WithTrace
// 时为 noop)。可用 WithoutOTel 关闭。Kafka 场景优先用本模块内置 kotel,不必再套
// pkg/mq/otelmq(避免双重 Inject);otelmq 仍适用于 InProc/NATS 等非 franz 传输。
//
// 注意:端到端需要真实 Kafka broker;本模块单测只覆盖消息映射与构造,broker 互操作请在
// 具备 Kafka 的环境验证。
package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

// ===== Publisher =====

// Publisher 实现 mq.Publisher(基于 franz-go Client,连接复用)。
type Publisher struct {
	cl *kgo.Client
}

var _ mq.Publisher = (*Publisher)(nil)

type publisherConfig struct {
	opts []kgo.Opt
	otel bool
}

// PublisherOption 配置 Publisher。
type PublisherOption func(*publisherConfig)

// WithClientOpts 透传任意 franz-go Opt(SASL、TLS、ClientID、压缩等)。
func WithClientOpts(opts ...kgo.Opt) PublisherOption {
	return func(c *publisherConfig) { c.opts = append(c.opts, opts...) }
}

// WithPartitioner 设置分区器(默认 StickyKeyPartitioner,同 Key 保序)。
func WithPartitioner(p kgo.Partitioner) PublisherOption {
	return func(c *publisherConfig) { c.opts = append(c.opts, kgo.RecordPartitioner(p)) }
}

// WithProduceTimeout 设置 Produce 请求超时。
func WithProduceTimeout(d time.Duration) PublisherOption {
	return func(c *publisherConfig) { c.opts = append(c.opts, kgo.ProduceRequestTimeout(d)) }
}

// WithoutOTel 关闭默认挂载的 kotel hooks。
func WithoutOTel() PublisherOption {
	return func(c *publisherConfig) { c.otel = false }
}

// NewPublisher 创建发布者。brokers 是 bootstrap 地址(如 []string{"127.0.0.1:9092"})。
// 默认启用 kotel OTel hooks。
func NewPublisher(brokers []string, opts ...PublisherOption) (*Publisher, error) {
	cfg := publisherConfig{otel: true}
	for _, o := range opts {
		o(&cfg)
	}
	kopts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)), // 同 Key 保序
	}
	kopts = append(kopts, cfg.opts...)
	if cfg.otel {
		kopts = append(kopts, kgo.WithHooks(defaultKotel().Hooks()...))
	}
	cl, err := kgo.NewClient(kopts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: new publisher: %w", err)
	}
	return &Publisher{cl: cl}, nil
}

// Client 返回底层 franz-go Client,供需要高级能力时使用。
func (p *Publisher) Client() *kgo.Client { return p.cl }

// Publish 实现 mq.Publisher(同步 Produce,便于错误回传)。
func (p *Publisher) Publish(ctx context.Context, msg mq.Message) error {
	r := toRecord(msg)
	r.Context = ctx // kotel publish span 挂到父 ctx
	if err := p.cl.ProduceSync(ctx, r).FirstErr(); err != nil {
		return fmt.Errorf("kafka: write %s: %w", msg.Topic, err)
	}
	return nil
}

// Close 关闭 client。
func (p *Publisher) Close() { p.cl.Close() }

// ===== Subscriber =====

// Subscriber 实现 mq.Subscriber。每个 Subscribe 起一个独立 consumer group client。
type Subscriber struct {
	brokers   []string
	minBytes  int32
	maxBytes  int32
	startEnd  bool
	otel      bool
	extraOpts []kgo.Opt
	wg        sync.WaitGroup
}

var _ mq.Subscriber = (*Subscriber)(nil)

// SubscriberOption 配置 Subscriber。
type SubscriberOption func(*Subscriber)

// WithFetchBounds 设置单次拉取字节范围(默认 1B~1MB)。
func WithFetchBounds(minBytes, maxBytes int) SubscriberOption {
	return func(s *Subscriber) {
		if minBytes > 0 {
			s.minBytes = int32(minBytes)
		}
		if maxBytes > 0 {
			s.maxBytes = int32(maxBytes)
		}
	}
}

// WithStartFromFirst 无已提交位点时从最早消息开始消费(默认从最早,与 franz-go / Kafka 默认行为一致)。
// Deprecated: 默认已是 AtStart,无需调用。保留仅为兼容。
func WithStartFromFirst() SubscriberOption {
	return func(s *Subscriber) {}
}

// WithStartFromEnd 无已提交位点时从最新消息开始消费(忽略历史)。
func WithStartFromEnd() SubscriberOption {
	return func(s *Subscriber) { s.startEnd = true }
}

// WithSubscriberClientOpts 透传任意 franz-go Opt 到每次 Subscribe 创建的 client。
func WithSubscriberClientOpts(opts ...kgo.Opt) SubscriberOption {
	return func(s *Subscriber) { s.extraOpts = append(s.extraOpts, opts...) }
}

// WithoutSubscriberOTel 关闭消费端默认 kotel hooks 与 process span。
func WithoutSubscriberOTel() SubscriberOption {
	return func(s *Subscriber) { s.otel = false }
}

// NewSubscriber 创建订阅者。
func NewSubscriber(brokers []string, opts ...SubscriberOption) *Subscriber {
	s := &Subscriber{
		brokers:  brokers,
		minBytes: 1,
		maxBytes: 1 << 20,
		otel:     true,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ErrGroupRequired 表示未提供 consumer group。Kafka 消费必须指定 group(见包注释)。
var ErrGroupRequired = errors.New("kafka: subscribe requires a consumer group (use mq.WithGroup)")

// Subscribe 实现 mq.Subscriber:为 topic 起一个 consumer group client,at-least-once
// (handler 成功后 CommitRecords),ctx 取消即停。必须经 mq.WithGroup 指定 group。
func (s *Subscriber) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	cfg := mq.ApplySubOptions(opts...)
	if cfg.Group == "" {
		return ErrGroupRequired
	}

	reset := kgo.NewOffset().AtStart()
	if s.startEnd {
		reset = kgo.NewOffset().AtEnd()
	}

	var tracer *kotel.Tracer
	kopts := []kgo.Opt{
		kgo.SeedBrokers(s.brokers...),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(reset),
		kgo.FetchMinBytes(s.minBytes),
		kgo.FetchMaxBytes(s.maxBytes),
	}
	kopts = append(kopts, s.extraOpts...)
	if s.otel {
		kt := kotel.NewTracer(kotel.ConsumerGroup(cfg.Group))
		tracer = kt
		kopts = append(kopts, kgo.WithHooks(kotel.NewKotel(kotel.WithTracer(kt), kotel.WithMeter(kotel.NewMeter())).Hooks()...))
	}

	cl, err := kgo.NewClient(kopts...)
	if err != nil {
		return fmt.Errorf("kafka: subscribe %s: %w", topic, err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cl.Close()
		s.consumeLoop(ctx, cl, tracer, topic, cfg.Group, h)
	}()
	return nil
}

func (s *Subscriber) consumeLoop(ctx context.Context, cl *kgo.Client, tracer *kotel.Tracer, topic, group string, h mq.Handler) {
	for {
		fetches := cl.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return
		}
		if err := ctx.Err(); err != nil {
			return
		}
		fetches.EachError(func(t string, p int32, err error) {
			if ctx.Err() == nil {
				slog.Debug("kafka: fetch", "topic", t, "partition", p, "group", group, "err", err)
			}
		})
		fetches.EachRecord(func(r *kgo.Record) {
			hctx := ctx
			if tracer != nil {
				var span trace.Span
				hctx, span = tracer.WithProcessSpan(r)
				defer span.End()
			}
			if herr := h(hctx, fromRecord(r)); herr != nil {
				if ctx.Err() == nil {
					slog.Debug("kafka: handler error, not committing", "topic", topic, "err", herr)
				}
				return
			}
			if err := cl.CommitRecords(ctx, r); err != nil && ctx.Err() == nil {
				slog.Debug("kafka: commit", "topic", topic, "err", err)
			}
		})
		cl.AllowRebalance()
	}
}

// Wait 阻塞直到所有 Subscribe 启动的消费 goroutine 退出(通常需先取消 Subscribe 的 ctx)。
func (s *Subscriber) Wait() { s.wg.Wait() }

// Close 等待所有消费 goroutine 退出。
func (s *Subscriber) Close() { s.Wait() }

// ===== kotel / 消息映射 =====

func defaultKotel() *kotel.Kotel {
	return kotel.NewKotel(
		kotel.WithTracer(kotel.NewTracer()),
		kotel.WithMeter(kotel.NewMeter()),
	)
}

func toRecord(msg mq.Message) *kgo.Record {
	r := &kgo.Record{Topic: msg.Topic, Value: msg.Body}
	if msg.Key != "" {
		r.Key = []byte(msg.Key)
	}
	if len(msg.Headers) > 0 {
		r.Headers = make([]kgo.RecordHeader, 0, len(msg.Headers))
		for k, v := range msg.Headers {
			r.Headers = append(r.Headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
		}
	}
	return r
}

func fromRecord(r *kgo.Record) mq.Message {
	msg := mq.Message{Topic: r.Topic, Key: string(r.Key), Body: r.Value}
	if len(r.Headers) > 0 {
		msg.Headers = make(map[string]string, len(r.Headers))
		for _, hh := range r.Headers {
			msg.Headers[hh.Key] = string(hh.Value)
		}
	}
	return msg
}
