//go:build integration

package redis_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	beautyredis "github.com/rushteam/beauty/pkg/infra/redis"
	"github.com/rushteam/beauty/pkg/messaging/mq"
	"github.com/rushteam/beauty/pkg/messaging/stream"
)

func newPubSub(t *testing.T, opts ...beautyredis.PubSubOption) *beautyredis.PubSub {
	t.Helper()
	p, err := beautyredis.NewPubSubFromConfig(&beautyredis.Config{Addr: redisAddr(t)}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func uniqueTopic(t *testing.T) string {
	return fmt.Sprintf("beauty:test:%s:%d", t.Name(), time.Now().UnixNano())
}

func recvMsg(t *testing.T, ch <-chan mq.Message) mq.Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for message")
	}
	return mq.Message{}
}

func collect(ch chan mq.Message) mq.Handler {
	return func(_ context.Context, m mq.Message) error {
		ch <- m
		return nil
	}
}

func numSub(t *testing.T, topic string) int64 {
	t.Helper()
	res, err := newRawClient(t).PubSubNumSub(context.Background(), topic).Result()
	if err != nil {
		t.Fatal(err)
	}
	return res[topic]
}

func waitNumSub(t *testing.T, topic string, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if numSub(t, topic) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("PUBSUB NUMSUB %s != %d", topic, want)
}

func TestPubSub_CrossNodeWithEnvelope(t *testing.T) {
	nodeA, nodeB := newPubSub(t), newPubSub(t)
	topic := uniqueTopic(t)
	got := make(chan mq.Message, 1)
	if err := nodeA.Subscribe(t.Context(), topic, collect(got)); err != nil {
		t.Fatal(err)
	}
	in := mq.Message{Topic: topic, Key: "k1", Headers: map[string]string{"trace": "t-1"}, Body: []byte("payload")}
	if err := nodeB.Publish(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	m := recvMsg(t, got)
	if m.Topic != topic || m.Key != "k1" || m.Headers["trace"] != "t-1" || string(m.Body) != "payload" {
		t.Fatalf("got %+v", m)
	}
}

func TestPubSub_RawInterop(t *testing.T) {
	p := newPubSub(t)
	topic := uniqueTopic(t)
	got := make(chan mq.Message, 1)
	if err := p.Subscribe(t.Context(), topic, collect(got)); err != nil {
		t.Fatal(err)
	}
	if err := newRawClient(t).Publish(t.Context(), topic, "hello").Err(); err != nil {
		t.Fatal(err)
	}
	if m := recvMsg(t, got); string(m.Body) != "hello" || m.Key != "" {
		t.Fatalf("got %+v", m)
	}
}

// Subscribe 返回即已被服务端确认：紧接着发布的消息必须可达。
func TestPubSub_SubscribeConfirmedBeforeReturn(t *testing.T) {
	p := newPubSub(t)
	for i := range 50 {
		topic := fmt.Sprintf("%s:%d", uniqueTopic(t), i)
		got := make(chan mq.Message, 1)
		ctx, cancel := context.WithCancel(t.Context())
		if err := p.Subscribe(ctx, topic, collect(got)); err != nil {
			t.Fatal(err)
		}
		if err := p.Publish(t.Context(), mq.Message{Topic: topic, Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		recvMsg(t, got)
		cancel()
	}
}

// 同 topic 多个订阅共享一次 SUBSCRIBE；最后一个退订后 UNSUBSCRIBE；退订后立即重订仍可达。
func TestPubSub_RefcountAndResubscribe(t *testing.T) {
	p := newPubSub(t)
	topic := uniqueTopic(t)
	a, b := make(chan mq.Message, 4), make(chan mq.Message, 4)
	ctxA, cancelA := context.WithCancel(t.Context())
	ctxB, cancelB := context.WithCancel(t.Context())
	if err := p.Subscribe(ctxA, topic, collect(a)); err != nil {
		t.Fatal(err)
	}
	if err := p.Subscribe(ctxB, topic, collect(b)); err != nil {
		t.Fatal(err)
	}
	waitNumSub(t, topic, 1)

	if err := p.Publish(t.Context(), mq.Message{Topic: topic, Body: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	recvMsg(t, a)
	recvMsg(t, b)

	cancelA()
	time.Sleep(50 * time.Millisecond)
	waitNumSub(t, topic, 1)
	cancelB()
	waitNumSub(t, topic, 0)

	for i := range 20 {
		c := make(chan mq.Message, 1)
		ctx, cancel := context.WithCancel(t.Context())
		if err := p.Subscribe(ctx, topic, collect(c)); err != nil {
			t.Fatal(err)
		}
		if err := p.Publish(t.Context(), mq.Message{Topic: topic, Body: []byte("again")}); err != nil {
			t.Fatal(err)
		}
		if m := recvMsg(t, c); string(m.Body) != "again" {
			t.Fatalf("round %d got %+v", i, m)
		}
		cancel()
	}
	waitNumSub(t, topic, 0)
}

// 慢 handler 只丢自己的消息，不阻塞同连接上其它 topic。
func TestPubSub_SlowHandlerDoesNotBlockOthers(t *testing.T) {
	p := newPubSub(t)
	slowTopic, fastTopic := uniqueTopic(t)+":slow", uniqueTopic(t)+":fast"
	block := make(chan struct{})
	defer close(block)
	err := p.Subscribe(t.Context(), slowTopic, func(ctx context.Context, _ mq.Message) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil
	}, mq.WithBuffer(1))
	if err != nil {
		t.Fatal(err)
	}
	fast := make(chan mq.Message, 1)
	if err := p.Subscribe(t.Context(), fastTopic, collect(fast)); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := p.Publish(t.Context(), mq.Message{Topic: slowTopic, Body: []byte("s")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Publish(t.Context(), mq.Message{Topic: fastTopic, Body: []byte("f")}); err != nil {
		t.Fatal(err)
	}
	recvMsg(t, fast)
	if p.Dropped() == 0 {
		t.Fatal("expected drops on the slow subscription")
	}
}

type roomEvent struct {
	Text string `json:"text"`
}

// stream.Bridge 以 Redis Pub/Sub 为 backplane 做跨节点房间广播。
func TestPubSub_StreamBridge(t *testing.T) {
	prefix := uniqueTopic(t) + ":room:"
	psA, psB := newPubSub(t), newPubSub(t)
	nodeA := stream.NewBridge(psA, psA, stream.TopicPrefix[string](prefix), stream.JSON[roomEvent]())
	nodeB := stream.NewBridge(psB, psB, stream.TopicPrefix[string](prefix), stream.JSON[roomEvent]())
	defer nodeA.Close()
	defer nodeB.Close()

	ch, cancel, err := nodeA.Subscribe(t.Context(), "lobby")
	if err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Publish(t.Context(), "lobby", roomEvent{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Text != "hi" {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for cross-node event")
	}
	cancel()
	waitNumSub(t, prefix+"lobby", 0)
}
