// Package redisstream 是 pkg/mq 的 Redis Streams broker 绑定,作为独立 Go 模块发布
// (github.com/rushteam/beauty/contrib/redisstream),不进 beauty 核心依赖图。基于
// redis/go-redis/v9 实现 mq.Publisher / mq.Subscriber。
//
// Redis Streams 适合中小规模、低延迟的消息场景,部署简单(复用已有 Redis),无需额外 broker。
//
// 语义映射:
//   - topic → Redis stream key;mq.Message.Key → 消息字段 "key"(保留在消息体内);
//     Body → 消息字段 "body";Headers → 消息字段 "headers"(JSON 编码);
//   - mq.WithGroup(g) → Redis consumer group(XREADGROUP 竞争消费,at-least-once);
//     无 group → XREAD 独立读取(每个订阅者读全部消息,扇出语义)。
//
// 投递保证:
//   - 有 group:at-least-once——handler 成功才 XACK;失败不确认,后续可通过 pending 重投。
//   - 无 group:at-most-once——XREAD 无确认机制,重启后从最新位置继续读。
//
// 注意:端到端需要真实 Redis 6.2+;本模块测试只覆盖消息映射与构造逻辑。
package redisstream

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/rushteam/beauty/pkg/mq"
)

// ===== Publisher =====

// Publisher 实现 mq.Publisher(XADD 写入 Redis Stream)。
type Publisher struct {
	rdb    redis.Cmdable
	maxLen int64
}

var _ mq.Publisher = (*Publisher)(nil)

// PublisherOption 配置 Publisher。
type PublisherOption func(*Publisher)

// WithMaxLen 设置 stream 的最大长度(MAXLEN ~,近似裁剪,防止无限增长)。0 表示不限。
func WithMaxLen(n int64) PublisherOption {
	return func(p *Publisher) { p.maxLen = n }
}

// NewPublisher 创建发布者。rdb 是任意 go-redis Cmdable(Client/ClusterClient/Ring)。
func NewPublisher(rdb redis.Cmdable, opts ...PublisherOption) *Publisher {
	p := &Publisher{rdb: rdb}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Publish 实现 mq.Publisher(XADD 到 stream key = msg.Topic)。
func (p *Publisher) Publish(ctx context.Context, msg mq.Message) error {
	values := map[string]interface{}{
		"body": string(msg.Body),
	}
	if msg.Key != "" {
		values["key"] = msg.Key
	}
	if len(msg.Headers) > 0 {
		hb, _ := json.Marshal(msg.Headers)
		values["headers"] = string(hb)
	}

	args := &redis.XAddArgs{
		Stream: msg.Topic,
		Values: values,
	}
	if p.maxLen > 0 {
		args.MaxLen = p.maxLen
		args.Approx = true
	}

	if err := p.rdb.XAdd(ctx, args).Err(); err != nil {
		return fmt.Errorf("redisstream: xadd %s: %w", msg.Topic, err)
	}
	return nil
}

// ===== Subscriber =====

// Subscriber 实现 mq.Subscriber。
type Subscriber struct {
	rdb       redis.Cmdable
	blockTime time.Duration
	batchSize int64
	wg        sync.WaitGroup
}

var _ mq.Subscriber = (*Subscriber)(nil)

// SubscriberOption 配置 Subscriber。
type SubscriberOption func(*Subscriber)

// WithBlockTime 设置 XREAD/XREADGROUP 的阻塞等待时长(默认 5s)。
func WithBlockTime(d time.Duration) SubscriberOption {
	return func(s *Subscriber) { s.blockTime = d }
}

// WithBatchSize 设置每次读取的最大消息数(默认 10)。
func WithBatchSize(n int64) SubscriberOption {
	return func(s *Subscriber) { s.batchSize = n }
}

// NewSubscriber 创建订阅者。
func NewSubscriber(rdb redis.Cmdable, opts ...SubscriberOption) *Subscriber {
	s := &Subscriber{rdb: rdb, blockTime: 5 * time.Second, batchSize: 10}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Subscribe 实现 mq.Subscriber。
// 有 group:XREADGROUP 竞争消费,自动创建 consumer group(如不存在);无 group:XREAD 扇出。
func (s *Subscriber) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	cfg := mq.ApplySubOptions(opts...)

	if cfg.Group != "" {
		if err := s.ensureGroup(ctx, topic, cfg.Group); err != nil {
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.consumeGroup(ctx, topic, cfg.Group, h)
		}()
	} else {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.consumeNoGroup(ctx, topic, h)
		}()
	}
	return nil
}

func (s *Subscriber) ensureGroup(ctx context.Context, stream, group string) error {
	err := s.rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !isBusyGroup(err) {
		return fmt.Errorf("redisstream: create group %q on %q: %w", group, stream, err)
	}
	return nil
}

func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}

func (s *Subscriber) consumeGroup(ctx context.Context, stream, group string, h mq.Handler) {
	consumer := fmt.Sprintf("beauty-%d", time.Now().UnixNano())
	for {
		if ctx.Err() != nil {
			return
		}
		streams, err := s.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    s.batchSize,
			Block:    s.blockTime,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if err == redis.Nil {
				continue
			}
			slog.Debug("redisstream: xreadgroup", "stream", stream, "group", group, "err", err)
			time.Sleep(time.Second)
			continue
		}

		for _, st := range streams {
			for _, xmsg := range st.Messages {
				msg := fromXMessage(xmsg, stream)
				if herr := h(ctx, msg); herr != nil {
					if ctx.Err() == nil {
						slog.Debug("redisstream: handler error, not acking", "stream", stream, "id", xmsg.ID, "err", herr)
					}
					continue
				}
				if err := s.rdb.XAck(ctx, stream, group, xmsg.ID).Err(); err != nil && ctx.Err() == nil {
					slog.Debug("redisstream: xack", "stream", stream, "id", xmsg.ID, "err", err)
				}
			}
		}
	}
}

func (s *Subscriber) consumeNoGroup(ctx context.Context, stream string, h mq.Handler) {
	lastID := "$"
	for {
		if ctx.Err() != nil {
			return
		}
		streams, err := s.rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{stream, lastID},
			Count:   s.batchSize,
			Block:   s.blockTime,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if err == redis.Nil {
				continue
			}
			slog.Debug("redisstream: xread", "stream", stream, "err", err)
			time.Sleep(time.Second)
			continue
		}

		for _, st := range streams {
			for _, xmsg := range st.Messages {
				lastID = xmsg.ID
				msg := fromXMessage(xmsg, stream)
				if herr := h(ctx, msg); herr != nil {
					if ctx.Err() == nil {
						slog.Debug("redisstream: handler error", "stream", stream, "id", xmsg.ID, "err", herr)
					}
				}
			}
		}
	}
}

// Wait 阻塞直到所有消费 goroutine 退出。
func (s *Subscriber) Wait() { s.wg.Wait() }

// Close 等待所有消费退出(需先取消 Subscribe 的 ctx)。
func (s *Subscriber) Close() { s.Wait() }

// ===== 消息映射 =====

func fromXMessage(xmsg redis.XMessage, stream string) mq.Message {
	msg := mq.Message{Topic: stream}
	if v, ok := xmsg.Values["body"]; ok {
		if s, ok := v.(string); ok {
			msg.Body = []byte(s)
		}
	}
	if v, ok := xmsg.Values["key"]; ok {
		if s, ok := v.(string); ok {
			msg.Key = s
		}
	}
	if v, ok := xmsg.Values["headers"]; ok {
		if s, ok := v.(string); ok {
			var headers map[string]string
			if json.Unmarshal([]byte(s), &headers) == nil {
				msg.Headers = headers
			}
		}
	}
	return msg
}

// ToXAddValues 导出消息映射逻辑(供测试用)。
func ToXAddValues(msg mq.Message) map[string]interface{} {
	values := map[string]interface{}{
		"body": string(msg.Body),
	}
	if msg.Key != "" {
		values["key"] = msg.Key
	}
	if len(msg.Headers) > 0 {
		hb, _ := json.Marshal(msg.Headers)
		values["headers"] = string(hb)
	}
	return values
}
