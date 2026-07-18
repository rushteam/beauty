// Package mq 提供传输无关的消息队列抽象:发布/订阅接口 + 一个「消费者即 beauty.Service」
// 的运行原语 + 处理中间件。补齐框架跨服务异步的空白——此前只有进程内 eventbus(泛型扇出)
// 与 webhook(HTTP 推),没有面向真 broker(NATS/Kafka/…)的统一抽象。
//
// 分层:
//   - Publisher / Subscriber:传输无关接口,由具体 broker 实现。本包自带零依赖的进程内实现
//     (NewInProc),用于单体/开发/测试;真 broker 作为 opt-in 子包实现同一接口(如未来的
//     pkg/infra/nats),不强引依赖。
//   - Consumer:把一组 (topic, handler) 订阅包成 beauty.Service(Start/String/Ready),
//     随 app 优雅停机。
//   - HandlerMiddleware:Recover(吞 panic)、Retry(瞬时错误重试)等,Chain 组合。
//
// 语义:
//   - 订阅按 ctx 绑定生命周期:Subscribe 传入的 ctx 取消即解除该订阅(不影响 broker 其它订阅)。
//   - Group(队列组):同一 (topic, group) 的多个订阅者**竞争消费**(每条消息只投给组内一个),
//     用于多副本水平扩展;不设 group 则**扇出**(每个订阅者都收到)。语义对齐 NATS queue group /
//     Kafka consumer group。
//   - 投递保证由 broker 决定:进程内实现是 at-most-once(handler 出错不重投,用 Retry 中间件兜
//     瞬时错误);要持久化/重投/exactly-once 用支持的 broker(如 JetStream)。
//
// 边界(机制而非策略):序列化(Body 是 []byte)、trace 透传(用 Headers 承载,配 pkg/metadata)、
// 分区键(Key)、broker 选型都是 policy。
package mq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrClosed 表示 broker 已关闭,不再接受发布/订阅。
var ErrClosed = errors.New("mq: broker closed")

// Message 是一条消息。Body 是已序列化的负载(编解码是 policy)。
type Message struct {
	Topic   string            // 主题(broker 的 subject/topic)
	Key     string            // 分区/有序键(可空;broker 用它做分区或有序投递)
	Body    []byte            // 负载
	Headers map[string]string // 元数据(content-type、trace 上下文等)
}

// Handler 处理一条消息。返回非 nil error 表示处理失败——具体后果(重投/丢弃)由 broker 与
// 中间件决定。
type Handler func(ctx context.Context, msg Message) error

// Publisher 是发布侧,由 broker 实现。
type Publisher interface {
	// Publish 发布一条消息;应并发安全。
	Publish(ctx context.Context, msg Message) error
}

// Subscriber 是订阅侧,由 broker 实现。
type Subscriber interface {
	// Subscribe 为 topic 注册 handler,订阅生命周期与 ctx 绑定(ctx 取消即解除该订阅)。
	// 通过 WithGroup 指定队列组做竞争消费。方法返回后投递即开始(在 broker 的 goroutine 上)。
	Subscribe(ctx context.Context, topic string, h Handler, opts ...SubscribeOption) error
}

// SubscribeOption 配置一次订阅。
type SubscribeOption func(*SubConfig)

// SubConfig 是订阅参数(供 broker 实现读取)。
type SubConfig struct {
	Group  string // 队列组:同组竞争消费;空则扇出
	Buffer int    // 投递缓冲(broker 实现可参考;<=0 用实现默认)
}

// WithGroup 设置队列组(同 (topic, group) 竞争消费,用于多副本水平扩展)。
func WithGroup(group string) SubscribeOption {
	return func(c *SubConfig) { c.Group = group }
}

// WithBuffer 设置该订阅的投递缓冲大小。
func WithBuffer(n int) SubscribeOption {
	return func(c *SubConfig) { c.Buffer = n }
}

// ApplySubOptions 供 broker 实现用:把 options 归一成 SubConfig。
func ApplySubOptions(opts ...SubscribeOption) SubConfig {
	cfg := SubConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// ===== 处理中间件 =====

// HandlerMiddleware 包装 Handler(如重试、恢复 panic、埋点)。
type HandlerMiddleware func(Handler) Handler

// Chain 按声明顺序把中间件套到 h 外层(第一个最外层,最先执行)。
func Chain(h Handler, mw ...HandlerMiddleware) Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// Recover 把 handler 里的 panic 转成 error,避免打崩投递 goroutine。
func Recover() HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, msg Message) (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("mq: handler panic: %v", r)
				}
			}()
			return next(ctx, msg)
		}
	}
}

// Retry 对返回 error 的处理重试至多 attempts 次,第 i 次失败后等 delay*(i+1)(线性退避);
// ctx 取消则立即返回。适合兜**瞬时**错误(下游抖动),不是持久化重投的替代。
func Retry(attempts int, delay time.Duration) HandlerMiddleware {
	if attempts < 1 {
		attempts = 1
	}
	return func(next Handler) Handler {
		return func(ctx context.Context, msg Message) error {
			var err error
			for i := 0; i < attempts; i++ {
				if err = next(ctx, msg); err == nil {
					return nil
				}
				if i == attempts-1 {
					break
				}
				select {
				case <-time.After(delay * time.Duration(i+1)):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return err
		}
	}
}

// ===== 消费者即 Service =====

// Consumer 把一组订阅包成 beauty.Service:Start 时全部 Subscribe,随后阻塞到 ctx 取消
// (各订阅随之解除)。结构上满足 beauty.Service(Start/String)+ ReadyNotifier(Ready)。
// 零值不可用,用 NewConsumer 构造;用 Handle 链式登记订阅。
type Consumer struct {
	sub  Subscriber
	name string
	regs []registration

	ready     chan struct{}
	readyOnce sync.Once
}

type registration struct {
	topic string
	h     Handler
	opts  []SubscribeOption
}

// ConsumerOption 配置 Consumer。
type ConsumerOption func(*Consumer)

// WithConsumerName 设置消费者名(日志/标识用)。
func WithConsumerName(name string) ConsumerOption {
	return func(c *Consumer) {
		if name != "" {
			c.name = name
		}
	}
}

// NewConsumer 创建消费者。sub 是任意 Subscriber 实现(进程内或 broker)。
func NewConsumer(sub Subscriber, opts ...ConsumerOption) *Consumer {
	c := &Consumer{sub: sub, name: "mq", ready: make(chan struct{})}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Handle 登记一个订阅(链式);在 Start 时统一 Subscribe。
func (c *Consumer) Handle(topic string, h Handler, opts ...SubscribeOption) *Consumer {
	c.regs = append(c.regs, registration{topic: topic, h: h, opts: opts})
	return c
}

// Start 订阅所有登记的 topic,然后阻塞到 ctx 取消——满足 beauty.Service。
func (c *Consumer) Start(ctx context.Context) error {
	for _, r := range c.regs {
		if err := c.sub.Subscribe(ctx, r.topic, r.h, r.opts...); err != nil {
			return fmt.Errorf("mq: subscribe %q: %w", r.topic, err)
		}
	}
	c.readyOnce.Do(func() { close(c.ready) })
	<-ctx.Done()
	return nil
}

// Ready 在所有订阅完成后关闭——满足 beauty.ReadyNotifier。
func (c *Consumer) Ready() <-chan struct{} { return c.ready }

// String 满足 beauty.Service。
func (c *Consumer) String() string { return "mq.Consumer(" + c.name + ")" }
