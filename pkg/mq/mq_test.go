package mq_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/mq"
)

func msg(topic, body string) mq.Message { return mq.Message{Topic: topic, Body: []byte(body)} }

// TestInProc_Fanout:不设 group 的多个订阅者都收到每条消息。
func TestInProc_Fanout(t *testing.T) {
	b := mq.NewInProc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var a, c atomic.Int64
	done := make(chan struct{}, 2)
	_ = b.Subscribe(ctx, "t", func(_ context.Context, _ mq.Message) error { a.Add(1); done <- struct{}{}; return nil })
	_ = b.Subscribe(ctx, "t", func(_ context.Context, _ mq.Message) error { c.Add(1); done <- struct{}{}; return nil })

	if err := b.Publish(ctx, msg("t", "x")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for range 2 {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("超时:扇出未送达两个订阅者")
		}
	}
	if a.Load() != 1 || c.Load() != 1 {
		t.Fatalf("扇出计数 a=%d c=%d, want 1/1", a.Load(), c.Load())
	}
}

// TestInProc_QueueGroup:同 group 的订阅者竞争消费,每条消息只投一个;两者合计收全。
func TestInProc_QueueGroup(t *testing.T) {
	b := mq.NewInProc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 100
	var got atomic.Int64
	var wg sync.WaitGroup
	wg.Add(n)
	h := func(_ context.Context, _ mq.Message) error { got.Add(1); wg.Done(); return nil }
	_ = b.Subscribe(ctx, "jobs", h, mq.WithGroup("workers"))
	_ = b.Subscribe(ctx, "jobs", h, mq.WithGroup("workers"))

	for i := range n {
		if err := b.Publish(ctx, msg("jobs", string(rune(i)))); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	waitWG(t, &wg, 3*time.Second)
	if got.Load() != n {
		t.Fatalf("竞争消费合计 = %d, want %d(每条只投一个,不重不漏)", got.Load(), n)
	}
}

// TestInProc_UnsubscribeOnCtx:ctx 取消后订阅解除,不再收消息。
func TestInProc_UnsubscribeOnCtx(t *testing.T) {
	b := mq.NewInProc()
	subCtx, unsub := context.WithCancel(context.Background())
	var got atomic.Int64
	recv := make(chan struct{}, 1)
	_ = b.Subscribe(subCtx, "t", func(_ context.Context, _ mq.Message) error {
		got.Add(1)
		select {
		case recv <- struct{}{}:
		default:
		}
		return nil
	})

	_ = b.Publish(context.Background(), msg("t", "1"))
	select {
	case <-recv:
	case <-time.After(2 * time.Second):
		t.Fatal("首条未送达")
	}

	unsub()                            // 解除订阅
	time.Sleep(100 * time.Millisecond) // 让解除生效
	_ = b.Publish(context.Background(), msg("t", "2"))
	time.Sleep(100 * time.Millisecond)
	if got.Load() != 1 {
		t.Fatalf("解除后仍收到消息, got=%d want 1", got.Load())
	}
}

// TestConsumer_ServiceLifecycle:Consumer 作为 Service,Start 后 Ready,ctx 取消后退出。
func TestConsumer_ServiceLifecycle(t *testing.T) {
	b := mq.NewInProc()
	var got atomic.Int64
	recv := make(chan struct{}, 8)
	c := mq.NewConsumer(b, mq.WithConsumerName("test")).
		Handle("orders", func(_ context.Context, _ mq.Message) error {
			got.Add(1)
			recv <- struct{}{}
			return nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- c.Start(ctx) }()

	select {
	case <-c.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("Consumer 未就绪")
	}

	_ = b.Publish(context.Background(), msg("orders", "o1"))
	select {
	case <-recv:
	case <-time.After(2 * time.Second):
		t.Fatal("消费者未收到消息")
	}

	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Start 返回 error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Start 未退出")
	}
	if c.String() != "mq.Consumer(test)" {
		t.Fatalf("String = %q", c.String())
	}
}

// TestRetry:瞬时错误重试,最终成功;Recover 吞 panic。
func TestRetryAndRecover(t *testing.T) {
	var calls atomic.Int64
	h := mq.Chain(func(_ context.Context, _ mq.Message) error {
		if calls.Add(1) < 3 {
			return errors.New("transient")
		}
		return nil
	}, mq.Recover(), mq.Retry(5, time.Millisecond))

	if err := h(context.Background(), msg("t", "x")); err != nil {
		t.Fatalf("重试后应成功, got %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("调用 %d 次, want 3", calls.Load())
	}

	// panic 被 Recover 转成 error(不打崩)。
	ph := mq.Chain(func(_ context.Context, _ mq.Message) error { panic("boom") }, mq.Recover())
	if err := ph(context.Background(), msg("t", "x")); err == nil {
		t.Fatal("panic 应被 Recover 转成 error")
	}
}

// TestInProc_Closed:关闭后发布/订阅返回 ErrClosed。
func TestInProc_Closed(t *testing.T) {
	b := mq.NewInProc()
	_ = b.Close()
	if err := b.Publish(context.Background(), msg("t", "x")); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("closed publish err = %v, want ErrClosed", err)
	}
	if err := b.Subscribe(context.Background(), "t", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("closed subscribe err = %v, want ErrClosed", err)
	}
}

func waitWG(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("等待超时")
	}
}
