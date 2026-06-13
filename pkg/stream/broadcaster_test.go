package stream

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBroadcaster_FanOut(t *testing.T) {
	b := New[int]()
	defer b.Close()

	ch1, c1 := b.Subscribe(context.Background())
	ch2, c2 := b.Subscribe(context.Background())
	defer c1()
	defer c2()

	if n := b.Publish(42); n != 2 {
		t.Fatalf("want delivered to 2, got %d", n)
	}
	if v := <-ch1; v != 42 {
		t.Fatalf("sub1 got %d", v)
	}
	if v := <-ch2; v != 42 {
		t.Fatalf("sub2 got %d", v)
	}
}

func TestBroadcaster_UnsubscribeClosesChannel(t *testing.T) {
	b := New[int]()
	defer b.Close()

	ch, cancel := b.Subscribe(context.Background())
	if b.SubscriberCount() != 1 {
		t.Fatalf("want 1 sub, got %d", b.SubscriberCount())
	}
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
	if b.SubscriberCount() != 0 {
		t.Fatalf("want 0 subs after cancel, got %d", b.SubscriberCount())
	}
}

func TestBroadcaster_CtxCancelUnsubscribes(t *testing.T) {
	b := New[int]()
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := b.Subscribe(ctx)
	cancel()

	// channel 应在 ctx 取消后被关闭
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("ctx cancel did not unsubscribe in time")
	}
}

func TestBroadcaster_SlowSubscriberDropsOldest(t *testing.T) {
	b := New[int](WithBufferSize(2), WithDropMode(DropOldest))
	defer b.Close()

	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	// 不消费，连发 5 条；容量 2，丢最旧，最终应保留最后 2 条 [3,4]
	for i := range 5 {
		b.Publish(i)
	}
	var got []int
	for len(ch) > 0 {
		got = append(got, <-ch)
	}
	if len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("DropOldest should keep newest 2 [3 4], got %v", got)
	}
}

func TestBroadcaster_PublishNeverBlocks(t *testing.T) {
	b := New[int](WithBufferSize(1))
	defer b.Close()
	_, cancel := b.Subscribe(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := range 10000 {
			b.Publish(i) // 订阅者不消费，仍不能阻塞
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}

func TestBroadcaster_CloseClosesAll(t *testing.T) {
	b := New[int]()
	ch1, _ := b.Subscribe(context.Background())
	ch2, _ := b.Subscribe(context.Background())
	b.Close()

	for _, ch := range []<-chan int{ch1, ch2} {
		if _, ok := <-ch; ok {
			t.Fatal("all channels should be closed after Close")
		}
	}
	if n := b.Publish(1); n != 0 {
		t.Fatalf("Publish after Close should deliver to 0, got %d", n)
	}
	// Subscribe after Close → 已关闭 channel
	ch, _ := b.Subscribe(context.Background())
	if _, ok := <-ch; ok {
		t.Fatal("Subscribe after Close should return closed channel")
	}
}

func TestBroadcaster_Concurrent(t *testing.T) {
	b := New[int](WithBufferSize(64))
	defer b.Close()

	var wg sync.WaitGroup
	// 并发订阅/退订
	for range 20 {
		wg.Go(func() {
			ctx, cancel := context.WithCancel(context.Background())
			ch, _ := b.Subscribe(ctx)
			go func() {
				for range ch {
				}
			}()
			time.Sleep(5 * time.Millisecond)
			cancel()
		})
	}
	// 并发发布
	for range 5 {
		wg.Go(func() {
			for i := range 200 {
				b.Publish(i)
			}
		})
	}
	wg.Wait()
}
