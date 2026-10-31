package stream

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

type event struct {
	Room string `json:"room"`
	Text string `json:"text"`
}

// countingSub 包装 Subscriber，记录订阅次数与仍然有效（ctx 未取消）的订阅数；
// gate 非 nil 时 Subscribe 阻塞到 gate 关闭，fail 非 nil 时返回该错误。
type countingSub struct {
	mq.Subscriber
	calls  atomic.Int32
	active atomic.Int32
	gate   chan struct{}
	fail   error
}

func (s *countingSub) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	s.calls.Add(1)
	if s.gate != nil {
		<-s.gate
	}
	if s.fail != nil {
		return s.fail
	}
	if err := s.Subscriber.Subscribe(ctx, topic, h, opts...); err != nil {
		return err
	}
	s.active.Add(1)
	go func() {
		<-ctx.Done()
		s.active.Add(-1)
	}()
	return nil
}

// 两个节点共享一个 backplane：任一节点发布，所有节点上该 key 的订阅者都收到，包括发布者本节点。
func TestBridge_CrossNode(t *testing.T) {
	bus := mq.NewInProc()
	defer bus.Close()
	topic := TopicPrefix[string]("sse.room.")
	nodeA := NewBridge(bus, bus, topic, JSON[event]())
	nodeB := NewBridge(bus, bus, topic, JSON[event]())
	defer nodeA.Close()
	defer nodeB.Close()

	ctx := context.Background()
	onA, cancelA, err := nodeA.Subscribe(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelA()
	onB, cancelB, err := nodeB.Subscribe(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelB()
	other, cancelOther, err := nodeA.Subscribe(ctx, "r2")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelOther()

	if err := nodeB.Publish(ctx, "r1", event{Room: "r1", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	for name, ch := range map[string]<-chan event{"nodeA": onA, "nodeB": onB} {
		if got := recvWithin(t, ch); got.Text != "hi" {
			t.Fatalf("%s got %+v", name, got)
		}
	}
	select {
	case v := <-other:
		t.Fatalf("r2 must not receive r1 message: %+v", v)
	case <-time.After(50 * time.Millisecond):
	}
}

// 同 key 多个本地订阅者只占一个 mq 订阅；最后一个离开时退订。
func TestBridge_SubscribesOncePerKey(t *testing.T) {
	bus := mq.NewInProc()
	defer bus.Close()
	sub := &countingSub{Subscriber: bus}
	b := NewBridge(bus, sub, TopicPrefix[string]("t."), JSON[event]())
	defer b.Close()

	ctx := context.Background()
	_, c1, err := b.Subscribe(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	_, c2, err := b.Subscribe(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if sub.calls.Load() != 1 || b.Len() != 1 {
		t.Fatalf("mq subscribes=%d len=%d, want 1/1", sub.calls.Load(), b.Len())
	}
	c1()
	if sub.active.Load() != 1 {
		t.Fatal("mq subscription must stay while a local subscriber remains")
	}
	c2()
	waitCond(t, "mq unsubscribe after last local subscriber", func() bool { return sub.active.Load() == 0 })
	if b.Len() != 0 {
		t.Fatalf("len=%d, want 0", b.Len())
	}

	// 回收后再次订阅会重新向 mq 订阅
	_, c3, err := b.Subscribe(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer c3()
	if sub.calls.Load() != 2 {
		t.Fatalf("mq subscribes=%d, want 2 after re-activation", sub.calls.Load())
	}
}

// 首个订阅者激活期间到来的同 key 订阅者等待同一次激活，不重复订阅 mq。
func TestBridge_ConcurrentFirstSubscribers(t *testing.T) {
	bus := mq.NewInProc()
	defer bus.Close()
	sub := &countingSub{Subscriber: bus, gate: make(chan struct{})}
	b := NewBridge(bus, sub, TopicPrefix[string]("t."), JSON[event]())
	defer b.Close()

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	cancels := make(chan func(), n)
	for range n {
		wg.Go(func() {
			_, cancel, err := b.Subscribe(context.Background(), "k")
			errs <- err
			if err == nil {
				cancels <- cancel
			}
		})
	}
	waitCond(t, "activation started", func() bool { return sub.calls.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	close(sub.gate)
	wg.Wait()
	close(errs)
	close(cancels)
	for err := range errs {
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
	if sub.calls.Load() != 1 || b.SubscriberCount("k") != n {
		t.Fatalf("mq subscribes=%d local=%d, want 1/%d", sub.calls.Load(), b.SubscriberCount("k"), n)
	}
	for c := range cancels {
		c()
	}
}

// mq 订阅失败：Subscribe 返回错误、不留残余条目，下次订阅重新尝试。
func TestBridge_ActivationFailure(t *testing.T) {
	bus := mq.NewInProc()
	defer bus.Close()
	boom := errors.New("broker down")
	sub := &countingSub{Subscriber: bus, fail: boom}
	b := NewBridge(bus, sub, TopicPrefix[string]("t."), JSON[event]())
	defer b.Close()

	if _, _, err := b.Subscribe(context.Background(), "k"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if b.Len() != 0 {
		t.Fatalf("failed key must not linger, len=%d", b.Len())
	}
	sub.fail = nil
	_, cancel, err := b.Subscribe(context.Background(), "k")
	if err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	defer cancel()
	if sub.calls.Load() != 2 {
		t.Fatalf("mq subscribes=%d, want 2", sub.calls.Load())
	}
}

// 无法解码的消息被丢弃，不影响后续消息。
func TestBridge_DecodeErrorSkipped(t *testing.T) {
	bus := mq.NewInProc()
	defer bus.Close()
	b := NewBridge(bus, bus, TopicPrefix[string]("t."), JSON[event]())
	defer b.Close()

	ch, cancel, err := b.Subscribe(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := bus.Publish(context.Background(), mq.Message{Topic: "t.k", Body: []byte("not-json")}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), "k", event{Text: "ok"}); err != nil {
		t.Fatal(err)
	}
	if got := recvWithin(t, ch); got.Text != "ok" {
		t.Fatalf("got %+v, want ok", got)
	}
}

func TestBridge_Close(t *testing.T) {
	bus := mq.NewInProc()
	defer bus.Close()
	sub := &countingSub{Subscriber: bus}
	b := NewBridge(bus, sub, TopicPrefix[string]("t."), Raw())

	ch, cancel, err := b.Subscribe(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	b.Close()
	if _, ok := <-ch; ok {
		t.Fatal("subscriber channel should be closed by Close")
	}
	cancel()
	waitCond(t, "mq unsubscribe on Close", func() bool { return sub.active.Load() == 0 })
	if _, _, err := b.Subscribe(context.Background(), "k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Subscribe after Close: %v", err)
	}
	if err := b.Publish(context.Background(), "k", []byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish after Close: %v", err)
	}
}
