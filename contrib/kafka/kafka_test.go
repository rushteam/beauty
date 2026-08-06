package kafka

import (
	"context"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/rushteam/beauty/pkg/mq"
)

// 消息映射:mq.Message ↔ kgo.Record 往返无损(Topic/Key/Body/Headers)。
func TestMessageMapping(t *testing.T) {
	in := mq.Message{
		Topic:   "orders",
		Key:     "user-7",
		Body:    []byte("payload"),
		Headers: map[string]string{"content-type": "application/json", "trace": "abc"},
	}
	r := toRecord(in)
	if r.Topic != "orders" || string(r.Key) != "user-7" || string(r.Value) != "payload" {
		t.Fatalf("toRecord 基本字段错误: %+v", r)
	}
	if len(r.Headers) != 2 {
		t.Fatalf("headers 数 = %d, want 2", len(r.Headers))
	}

	out := fromRecord(&kgo.Record{Topic: r.Topic, Key: r.Key, Value: r.Value, Headers: r.Headers})
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
	s := NewSubscriber([]string{"127.0.0.1:9092"}, WithoutSubscriberOTel())
	err := s.Subscribe(context.Background(), "t", func(context.Context, mq.Message) error { return nil })
	if !errors.Is(err, ErrGroupRequired) {
		t.Fatalf("无 group 应返回 ErrGroupRequired, got %v", err)
	}
}

// 接口断言:Publisher/Subscriber 满足 mq 接口。
func TestImplementsMQ(t *testing.T) {
	pub, err := NewPublisher([]string{"127.0.0.1:9092"}, WithoutOTel())
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()
	var _ mq.Publisher = pub
	var _ mq.Subscriber = NewSubscriber([]string{"127.0.0.1:9092"})
}

func TestNewPublisher(t *testing.T) {
	pub, err := NewPublisher([]string{"127.0.0.1:9092"}, WithoutOTel())
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()
	if pub.Client() == nil {
		t.Fatal("Client() is nil")
	}
}

func TestNewPublisher_EmptyBrokers(t *testing.T) {
	_, err := NewPublisher(nil, WithoutOTel())
	if err == nil {
		t.Fatal("empty brokers should error")
	}
}
