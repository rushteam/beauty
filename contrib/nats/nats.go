// Package nats 是 pkg/mq 的 NATS broker 绑定,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/nats),不进 beauty 核心依赖图。实现 mq.Publisher /
// mq.Subscriber:核心出接口(pkg/mq),本模块出实现——用 NATS 承载跨服务异步。
//
// 语义映射:
//   - topic → NATS subject;
//   - mq.WithGroup(g) → NATS queue group(同组竞争消费,天然对齐);不设组 → 普通订阅(扇出);
//   - Headers → NATS 消息头;Key 走 "X-MQ-Key" 头透传(NATS core 无分区键概念)。
//
// 投递保证:NATS core 是 at-most-once(不持久、不重投),与 mq 进程内实现一致;要持久化/重投
// 用 JetStream(可另起 contrib/natsjs)。订阅生命周期绑定 Subscribe 传入的 ctx(取消即退订)。
package nats

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/rushteam/beauty/pkg/mq"
)

const keyHeader = "X-MQ-Key"

// Conn 包一层 *nats.Conn,实现 mq.Publisher 与 mq.Subscriber。
type Conn struct {
	nc *nats.Conn
}

var (
	_ mq.Publisher  = (*Conn)(nil)
	_ mq.Subscriber = (*Conn)(nil)
)

// Connect 按 URL 连接 NATS(url 形如 "nats://127.0.0.1:4222"),opts 透传给 nats.Connect。
func Connect(url string, opts ...nats.Option) (*Conn, error) {
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats: connect %s: %w", url, err)
	}
	return &Conn{nc: nc}, nil
}

// New 用一个已建立的 *nats.Conn 构造(便于复用连接 / 自定义连接参数)。
func New(nc *nats.Conn) *Conn { return &Conn{nc: nc} }

// NATS 返回底层连接,供需要高级能力(JetStream、请求响应等)时使用。
func (c *Conn) NATS() *nats.Conn { return c.nc }

// Close 关闭连接。
func (c *Conn) Close() { c.nc.Close() }

// Publish 实现 mq.Publisher。
func (c *Conn) Publish(_ context.Context, msg mq.Message) error {
	m := &nats.Msg{Subject: msg.Topic, Data: msg.Body, Header: nats.Header{}}
	for k, v := range msg.Headers {
		m.Header.Set(k, v)
	}
	if msg.Key != "" {
		m.Header.Set(keyHeader, msg.Key)
	}
	if err := c.nc.PublishMsg(m); err != nil {
		return fmt.Errorf("nats: publish %s: %w", msg.Topic, err)
	}
	return nil
}

// Subscribe 实现 mq.Subscriber:WithGroup 用 queue 订阅(竞争消费),否则普通订阅(扇出)。
// 订阅随 ctx 取消而退订。
func (c *Conn) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	cfg := mq.ApplySubOptions(opts...)
	cb := func(m *nats.Msg) {
		if err := h(ctx, fromNATS(m)); err != nil && ctx.Err() == nil {
			slog.Debug("nats: handler error", "subject", topic, "group", cfg.Group, "err", err)
		}
	}
	var (
		sub *nats.Subscription
		err error
	)
	if cfg.Group != "" {
		sub, err = c.nc.QueueSubscribe(topic, cfg.Group, cb)
	} else {
		sub, err = c.nc.Subscribe(topic, cb)
	}
	if err != nil {
		return fmt.Errorf("nats: subscribe %s: %w", topic, err)
	}
	go func() {
		<-ctx.Done()
		_ = sub.Unsubscribe()
	}()
	return nil
}

func fromNATS(m *nats.Msg) mq.Message {
	msg := mq.Message{Topic: m.Subject, Body: m.Data}
	if len(m.Header) > 0 {
		msg.Headers = make(map[string]string, len(m.Header))
		for k := range m.Header {
			if k == keyHeader {
				msg.Key = m.Header.Get(k)
				continue
			}
			msg.Headers[k] = m.Header.Get(k)
		}
	}
	return msg
}
