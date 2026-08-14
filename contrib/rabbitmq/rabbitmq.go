// Package rabbitmq 是 pkg/mq 的 RabbitMQ (AMQP 0-9-1) broker 绑定,作为独立 Go 模块发布
// (github.com/rushteam/beauty/contrib/rabbitmq),不进 beauty 核心依赖图。基于官方
// rabbitmq/amqp091-go 实现 mq.Publisher / mq.Subscriber。
//
// 语义映射:
//   - topic → RabbitMQ routing key(发往 exchange);mq.Message.Key → 同 routing key(若非空则覆盖 topic);
//     Headers → AMQP headers table;Body → AMQP body。
//   - mq.WithGroup(g) → 使用命名队列 g 做竞争消费(多订阅者绑定同一队列);无 group 则自动生成
//     exclusive 临时队列做扇出(广播)。
//
// Exchange:默认 topic exchange(可配),Publisher 发往该 exchange + routing key;Subscriber 按
// group 是否为空决定声明 exclusive 或命名 durable 队列,再 bind routing key。
//
// 投递保证:Publisher 支持 confirm 模式(WithConfirm),Subscriber 为 at-least-once(handler 成功
// 才 Ack,失败 Nack+requeue)。
//
// 注意:端到端需要真实 RabbitMQ;本模块测试只覆盖消息映射与构造逻辑。
package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/rushteam/beauty/pkg/mq"
)

// ===== Publisher =====

// Publisher 实现 mq.Publisher(基于单连接单 channel,confirm 模式可选;断线自动重连一次)。
type Publisher struct {
	url  string
	cfg  publisherConfig
	conn *amqp.Connection
	ch   *amqp.Channel
	mu   sync.Mutex
}

var _ mq.Publisher = (*Publisher)(nil)

type publisherConfig struct {
	exchange   string
	confirm    bool
	declareExc bool
	excType    string
}

// PublisherOption 配置 Publisher。
type PublisherOption func(*publisherConfig)

// WithExchange 指定发布到的 exchange(默认 "beauty.topic")。
func WithExchange(name string) PublisherOption {
	return func(c *publisherConfig) { c.exchange = name }
}

// WithConfirm 启用 publisher confirm 模式(同步等待 broker 确认)。
func WithConfirm() PublisherOption {
	return func(c *publisherConfig) { c.confirm = true }
}

// WithExchangeType 设置 exchange 类型(默认 "topic")。若为空则不声明 exchange。
func WithExchangeType(t string) PublisherOption {
	return func(c *publisherConfig) { c.excType = t }
}

// WithDeclareExchange 是否在连接时自动声明 exchange(默认 true)。
func WithDeclareExchange(b bool) PublisherOption {
	return func(c *publisherConfig) { c.declareExc = b }
}

// NewPublisher 创建发布者。url 是 AMQP 连接串(如 "amqp://guest:guest@localhost:5672/")。
func NewPublisher(url string, opts ...PublisherOption) (*Publisher, error) {
	cfg := publisherConfig{exchange: "beauty.topic", excType: "topic", declareExc: true}
	for _, o := range opts {
		o(&cfg)
	}
	p := &Publisher{url: url, cfg: cfg}
	if err := p.reconnectLocked(); err != nil {
		return nil, err
	}
	return p, nil
}

func dialPublisher(url string, cfg publisherConfig) (conn *amqp.Connection, ch *amqp.Channel, err error) {
	conn, err = amqp.Dial(url)
	if err != nil {
		return nil, nil, fmt.Errorf("rabbitmq: dial: %w", err)
	}
	ch, err = conn.Channel()
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}
	if cfg.declareExc && cfg.excType != "" {
		if err := ch.ExchangeDeclare(cfg.exchange, cfg.excType, true, false, false, false, nil); err != nil {
			ch.Close()
			conn.Close()
			return nil, nil, fmt.Errorf("rabbitmq: declare exchange %q: %w", cfg.exchange, err)
		}
	}
	if cfg.confirm {
		if err := ch.Confirm(false); err != nil {
			ch.Close()
			conn.Close()
			return nil, nil, fmt.Errorf("rabbitmq: enable confirm: %w", err)
		}
	}
	return conn, ch, nil
}

func (p *Publisher) reconnectLocked() error {
	if p.ch != nil {
		_ = p.ch.Close()
		p.ch = nil
	}
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
	conn, ch, err := dialPublisher(p.url, p.cfg)
	if err != nil {
		return err
	}
	p.conn, p.ch = conn, ch
	return nil
}

// Publish 实现 mq.Publisher。routing key 优先取 msg.Key,其次 msg.Topic。
func (p *Publisher) Publish(ctx context.Context, msg mq.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.publishLocked(ctx, msg); err != nil {
		if !isReconnectable(err) {
			return err
		}
		if recErr := p.reconnectLocked(); recErr != nil {
			return err
		}
		return p.publishLocked(ctx, msg)
	}
	return nil
}

func (p *Publisher) publishLocked(ctx context.Context, msg mq.Message) error {
	routingKey := msg.Topic
	if msg.Key != "" {
		routingKey = msg.Key
	}

	pub := amqp.Publishing{
		ContentType:  "application/octet-stream",
		Body:         msg.Body,
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
	}
	if ct, ok := msg.Headers["content-type"]; ok {
		pub.ContentType = ct
	}
	if len(msg.Headers) > 0 {
		pub.Headers = make(amqp.Table, len(msg.Headers))
		for k, v := range msg.Headers {
			pub.Headers[k] = v
		}
	}

	if p.cfg.confirm {
		dc, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, p.cfg.exchange, routingKey, false, false, pub)
		if err != nil {
			return fmt.Errorf("rabbitmq: publish %s: %w", routingKey, err)
		}
		acked, err := dc.WaitContext(ctx)
		if err != nil {
			return fmt.Errorf("rabbitmq: confirm wait: %w", err)
		}
		if !acked {
			return fmt.Errorf("rabbitmq: publish nacked by broker")
		}
		return nil
	}

	if err := p.ch.PublishWithContext(ctx, p.cfg.exchange, routingKey, false, false, pub); err != nil {
		return fmt.Errorf("rabbitmq: publish %s: %w", routingKey, err)
	}
	return nil
}

func isReconnectable(err error) bool {
	return errors.Is(err, amqp.ErrClosed)
}

// Close 关闭连接。
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil {
		p.ch.Close()
	}
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

// ===== Subscriber =====

// Subscriber 实现 mq.Subscriber。每次 Subscribe 创建一个 channel + consumer goroutine。
type Subscriber struct {
	url      string
	exchange string
	excType  string
	prefetch int
	conn     *amqp.Connection
	mu       sync.Mutex
	wg       sync.WaitGroup
}

var _ mq.Subscriber = (*Subscriber)(nil)

// SubscriberOption 配置 Subscriber。
type SubscriberOption func(*Subscriber)

// WithSubscriberExchange 设置订阅绑定的 exchange(默认 "beauty.topic")。
func WithSubscriberExchange(name string) SubscriberOption {
	return func(s *Subscriber) { s.exchange = name }
}

// WithPrefetch 设置每个消费 channel 的 prefetch count(默认 10)。
func WithPrefetch(n int) SubscriberOption {
	return func(s *Subscriber) { s.prefetch = n }
}

// NewSubscriber 创建订阅者。
func NewSubscriber(url string, opts ...SubscriberOption) (*Subscriber, error) {
	s := &Subscriber{url: url, exchange: "beauty.topic", excType: "topic", prefetch: 10}
	for _, o := range opts {
		o(s)
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: dial: %w", err)
	}
	s.conn = conn
	return s, nil
}

// Subscribe 实现 mq.Subscriber。
// 有 group: 声明 durable 命名队列(竞争消费);无 group: exclusive 临时队列(扇出)。
func (s *Subscriber) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	cfg := mq.ApplySubOptions(opts...)

	s.mu.Lock()
	ch, err := s.conn.Channel()
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("rabbitmq: open channel: %w", err)
	}

	if err := ch.Qos(s.prefetch, 0, false); err != nil {
		ch.Close()
		return fmt.Errorf("rabbitmq: qos: %w", err)
	}

	if err := ch.ExchangeDeclare(s.exchange, s.excType, true, false, false, false, nil); err != nil {
		ch.Close()
		return fmt.Errorf("rabbitmq: declare exchange: %w", err)
	}

	var queueName string
	var exclusive bool
	if cfg.Group != "" {
		queueName = cfg.Group
	} else {
		exclusive = true
	}

	q, err := ch.QueueDeclare(queueName, !exclusive, exclusive, exclusive, false, nil)
	if err != nil {
		ch.Close()
		return fmt.Errorf("rabbitmq: declare queue: %w", err)
	}

	if err := ch.QueueBind(q.Name, topic, s.exchange, false, nil); err != nil {
		ch.Close()
		return fmt.Errorf("rabbitmq: bind queue %q to %q: %w", q.Name, topic, err)
	}

	deliveries, err := ch.Consume(q.Name, "", false, exclusive, false, false, nil)
	if err != nil {
		ch.Close()
		return fmt.Errorf("rabbitmq: consume: %w", err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ch.Close()
		s.consumeLoop(ctx, deliveries, topic, h)
	}()
	return nil
}

func (s *Subscriber) consumeLoop(ctx context.Context, deliveries <-chan amqp.Delivery, topic string, h mq.Handler) {
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				return
			}
			msg := fromDelivery(d, topic)
			if err := h(ctx, msg); err != nil {
				if ctx.Err() == nil {
					slog.Debug("rabbitmq: handler error, nack+requeue", "topic", topic, "err", err)
				}
				_ = d.Nack(false, true)
				continue
			}
			_ = d.Ack(false)
		}
	}
}

// Wait 阻塞直到所有消费 goroutine 退出。
func (s *Subscriber) Wait() { s.wg.Wait() }

// Close 关闭连接并等待消费停止。
func (s *Subscriber) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		err := s.conn.Close()
		s.wg.Wait()
		return err
	}
	return nil
}

// ===== 消息映射 =====

func fromDelivery(d amqp.Delivery, fallbackTopic string) mq.Message {
	msg := mq.Message{
		Topic: d.RoutingKey,
		Body:  d.Body,
	}
	if msg.Topic == "" {
		msg.Topic = fallbackTopic
	}
	if len(d.Headers) > 0 {
		msg.Headers = make(map[string]string, len(d.Headers))
		for k, v := range d.Headers {
			if sv, ok := v.(string); ok {
				msg.Headers[k] = sv
			}
		}
	}
	if d.ContentType != "" {
		if msg.Headers == nil {
			msg.Headers = make(map[string]string, 1)
		}
		msg.Headers["content-type"] = d.ContentType
	}
	return msg
}
