package stream

import (
	"context"
	"sync"
	"testing"
	"time"
)

func recvWithin[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for value")
	}
	var zero T
	return zero
}

func waitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestHub_LazyCreateAndReclaim(t *testing.T) {
	h := NewHub[string, int]()
	defer h.Close()

	if n := h.Publish("room1", 1); n != 0 || h.Len() != 0 {
		t.Fatalf("publish to idle key: delivered=%d len=%d, want 0/0", n, h.Len())
	}

	a, cancelA := h.Subscribe(context.Background(), "room1")
	b, cancelB := h.Subscribe(context.Background(), "room1")
	c, cancelC := h.Subscribe(context.Background(), "room2")
	if h.Len() != 2 || h.SubscriberCount("room1") != 2 {
		t.Fatalf("len=%d room1=%d, want 2/2", h.Len(), h.SubscriberCount("room1"))
	}

	if n := h.Publish("room1", 7); n != 2 {
		t.Fatalf("delivered=%d, want 2", n)
	}
	if recvWithin(t, a) != 7 || recvWithin(t, b) != 7 {
		t.Fatal("room1 subscribers should receive 7")
	}
	select {
	case v := <-c:
		t.Fatalf("room2 must not receive room1 message, got %d", v)
	default:
	}

	cancelA()
	if h.Len() != 2 {
		t.Fatal("room1 still has a subscriber, must not be reclaimed")
	}
	cancelB()
	cancelB() // 幂等
	if h.Len() != 1 || h.SubscriberCount("room1") != 0 {
		t.Fatalf("room1 should be reclaimed: len=%d", h.Len())
	}
	if _, ok := <-a; ok {
		t.Fatal("canceled subscriber channel should be closed")
	}
	cancelC()
	if h.Len() != 0 {
		t.Fatalf("len=%d, want 0", h.Len())
	}
}

func TestHub_ContextCancelReleases(t *testing.T) {
	h := NewHub[string, int]()
	defer h.Close()
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := h.Subscribe(ctx, "k")
	cancel()
	waitCond(t, "key reclaimed after ctx cancel", func() bool { return h.Len() == 0 })
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after ctx cancel")
	}
}

func TestHub_Close(t *testing.T) {
	h := NewHub[string, int]()
	ch, cancel := h.Subscribe(context.Background(), "k")
	h.Close()
	h.Close()
	if _, ok := <-ch; ok {
		t.Fatal("subscriber channel should be closed by Close")
	}
	cancel() // Close 之后取消不应 panic
	ch2, _ := h.Subscribe(context.Background(), "k")
	if _, ok := <-ch2; ok {
		t.Fatal("Subscribe after Close should return closed channel")
	}
	if h.Publish("k", 1) != 0 || h.Len() != 0 {
		t.Fatal("Publish after Close should be no-op")
	}
}

func TestHub_ConcurrentChurn(t *testing.T) {
	h := NewHub[int, int](WithBufferSize(4))
	defer h.Close()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				key := (g + i) % 3
				ch, cancel := h.Subscribe(context.Background(), key)
				h.Publish(key, i)
				select {
				case <-ch:
				default:
				}
				cancel()
			}
		})
	}
	wg.Wait()
	if h.Len() != 0 {
		t.Fatalf("all keys should be reclaimed, len=%d", h.Len())
	}
}
