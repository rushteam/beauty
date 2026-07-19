package kafka

import (
	"context"
	"errors"
	"testing"

	"github.com/segmentio/kafka-go"

	"github.com/rushteam/beauty/pkg/mq"
)

// 消息映射:mq.Message ↔ kafka.Message 往返无损(Topic/Key/Body/Headers)。
func TestMessageMapping(t *testing.T) {
	in := mq.Message{
		Topic:   "orders",
		Key:     "user-7",
		Body:    []byte("payload"),
		Headers: map[string]string{"content-type": "application/json", "trace": "abc"},
	}
	km := toKafka(in)
	if km.Topic != "orders" || string(km.Key) != "user-7" || string(km.Value) != "payload" {
		t.Fatalf("toKafka 基本字段错误: %+v", km)
	}
	if len(km.Headers) != 2 {
		t.Fatalf("headers 数 = %d, want 2", len(km.Headers))
	}

	out := fromKafka(kafka.Message{Topic: km.Topic, Key: km.Key, Value: km.Value, Headers: km.Headers})
	if out.Topic != in.Topic || out.Key != in.Key || string(out.Body) != string(in.Body) {
		t.Fatalf("往返基本字段不一致: %+v", out)
	}
	for k, v := range in.Headers {
		if out.Headers[k] != v {
			t.Fatalf("header %q 往返丢失: got %q want %q", k, out.Headers[k], v)
		}
	}
}

// 无 consumer group 时 Subscribe 返回 ErrGroupRequired(Kafka 消费必须有 group)。
func TestSubscribe_RequiresGroup(t *testing.T) {
	s := NewSubscriber([]string{"127.0.0.1:9092"})
	err := s.Subscribe(context.Background(), "t", func(context.Context, mq.Message) error { return nil })
	if !errors.Is(err, ErrGroupRequired) {
		t.Fatalf("无 group 应返回 ErrGroupRequired, got %v", err)
	}
	// 带 group 不应因缺 group 报错(真正连接失败是运行时的事,这里只验证前置校验通过)。
	// 用可取消 ctx,测试结束时停掉后台 reader,避免其一直重连造成 goroutine 泄漏。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = s.Subscribe(ctx, "t", func(context.Context, mq.Message) error { return nil }, mq.WithGroup("g"))
	if errors.Is(err, ErrGroupRequired) {
		t.Fatal("带 group 不应返回 ErrGroupRequired")
	}
}

// 接口断言:Publisher/Subscriber 满足 mq 接口(编译期已保证,这里显式记录)。
func TestImplementsMQ(t *testing.T) {
	var _ mq.Publisher = NewPublisher([]string{"127.0.0.1:9092"})
	var _ mq.Subscriber = NewSubscriber([]string{"127.0.0.1:9092"})
}
