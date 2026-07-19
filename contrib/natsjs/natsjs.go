// Package natsjs 是 pkg/mq 的 NATS JetStream broker 绑定,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/natsjs)。相比 contrib/nats(NATS core,at-most-once),
// 本模块用 JetStream 提供**持久化 + at-least-once**:消息落盘、消费确认、失败重投、断线可续。
//
// 语义映射:
//   - topic → JetStream subject(须有 Stream 覆盖,用 EnsureStream 建);
//   - mq.WithGroup(g) → **durable consumer**(同名 durable 竞争消费,水平扩展、断线续消费);
//     不设组 → **ephemeral consumer**(每个订阅者各自一份 → 扇出);
//   - Headers → 消息头;Key 走 "X-MQ-Key" 头透传;
//   - 投递:AckExplicit——handler 成功 Ack、失败 Nak(立即重投)。故 handler 应幂等。
//
// 订阅随 Subscribe 传入的 ctx 取消而停止消费。
package natsjs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/rushteam/beauty/pkg/mq"
)

const keyHeader = "X-MQ-Key"

// Conn 包一层 NATS 连接与 JetStream 上下文,实现 mq.Publisher 与 mq.Subscriber。
type Conn struct {
	nc *nats.Conn
	js jetstream.JetStream
}

var (
	_ mq.Publisher  = (*Conn)(nil)
	_ mq.Subscriber = (*Conn)(nil)
)

// Connect 连接 NATS 并建立 JetStream 上下文。
func Connect(url string, opts ...nats.Option) (*Conn, error) {
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("natsjs: connect %s: %w", url, err)
	}
	return New(nc)
}

// New 用已建立的 *nats.Conn 构造 JetStream 绑定。
func New(nc *nats.Conn) (*Conn, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("natsjs: jetstream: %w", err)
	}
	return &Conn{nc: nc, js: js}, nil
}

// NATS 返回底层连接。
func (c *Conn) NATS() *nats.Conn { return c.nc }

// JetStream 返回底层 JetStream 上下文(供建流/消费者等高级操作)。
func (c *Conn) JetStream() jetstream.JetStream { return c.js }

// Close 关闭连接。
func (c *Conn) Close() { c.nc.Close() }

// EnsureStream 创建或更新一个 Stream(覆盖给定 subjects)。发布/订阅前须有 Stream 覆盖 topic。
func (c *Conn) EnsureStream(ctx context.Context, name string, subjects ...string) error {
	_, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     name,
		Subjects: subjects,
	})
	if err != nil {
		return fmt.Errorf("natsjs: ensure stream %s: %w", name, err)
	}
	return nil
}

// Publish 实现 mq.Publisher:持久化发布到 msg.Topic(须有 Stream 覆盖该 subject)。
func (c *Conn) Publish(ctx context.Context, msg mq.Message) error {
	m := &nats.Msg{Subject: msg.Topic, Data: msg.Body, Header: nats.Header{}}
	for k, v := range msg.Headers {
		m.Header.Set(k, v)
	}
	if msg.Key != "" {
		m.Header.Set(keyHeader, msg.Key)
	}
	if _, err := c.js.PublishMsg(ctx, m); err != nil {
		return fmt.Errorf("natsjs: publish %s: %w", msg.Topic, err)
	}
	return nil
}

// Subscribe 实现 mq.Subscriber:WithGroup 用同名 durable 竞争消费,否则 ephemeral 扇出。
// AckExplicit:handler 成功 Ack、失败 Nak 重投。订阅随 ctx 取消停止。
func (c *Conn) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	cfg := mq.ApplySubOptions(opts...)
	stream, err := c.js.StreamNameBySubject(ctx, topic)
	if err != nil {
		return fmt.Errorf("natsjs: no stream for subject %q (call EnsureStream first): %w", topic, err)
	}

	ccfg := jetstream.ConsumerConfig{
		FilterSubject: topic,
		AckPolicy:     jetstream.AckExplicitPolicy,
	}
	if cfg.Group != "" {
		ccfg.Durable = cfg.Group // 持久且共享:同组竞争消费、断线续消费
	}

	cons, err := c.js.CreateOrUpdateConsumer(ctx, stream, ccfg)
	if err != nil {
		return fmt.Errorf("natsjs: consumer on %s: %w", stream, err)
	}

	consumeCtx, err := cons.Consume(func(m jetstream.Msg) {
		if herr := h(ctx, fromJS(m)); herr != nil {
			if ctx.Err() == nil {
				slog.Debug("natsjs: handler error, nak", "subject", topic, "group", cfg.Group, "err", herr)
			}
			_ = m.Nak() // 失败:否定确认,重投
			return
		}
		_ = m.Ack()
	})
	if err != nil {
		return fmt.Errorf("natsjs: consume %s: %w", topic, err)
	}
	go func() {
		<-ctx.Done()
		consumeCtx.Stop()
	}()
	return nil
}

func fromJS(m jetstream.Msg) mq.Message {
	msg := mq.Message{Topic: m.Subject(), Body: m.Data()}
	if hdr := m.Headers(); len(hdr) > 0 {
		msg.Headers = make(map[string]string, len(hdr))
		for k := range hdr {
			if k == keyHeader {
				msg.Key = hdr.Get(k)
				continue
			}
			msg.Headers[k] = hdr.Get(k)
		}
	}
	return msg
}
