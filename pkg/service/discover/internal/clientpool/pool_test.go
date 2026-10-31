package clientpool

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClient struct {
	id     int
	closed atomic.Bool
}

type harness struct {
	pool    *Pool[*fakeClient]
	created atomic.Int32
	closedN atomic.Int32
	closed  chan *fakeClient
}

func newHarness(idle time.Duration) *harness {
	h := &harness{closed: make(chan *fakeClient, 16)}
	h.pool = New(idle, func(c *fakeClient) {
		if c.closed.Swap(true) {
			panic("client closed twice")
		}
		h.closedN.Add(1)
		select {
		case h.closed <- c:
		default:
		}
	})
	return h
}

func (h *harness) acquire(t *testing.T, key string) (*fakeClient, func()) {
	t.Helper()
	c, release, err := h.pool.Acquire(key, func() (*fakeClient, error) {
		return &fakeClient{id: int(h.created.Add(1))}, nil
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return c, release
}

func TestPool_SharesSameKey(t *testing.T) {
	h := newHarness(time.Hour)
	a, ra := h.acquire(t, "k")
	b, rb := h.acquire(t, "k")
	c, rc := h.acquire(t, "other")
	defer ra()
	defer rb()
	defer rc()

	if a != b {
		t.Fatal("same key should share one client")
	}
	if a == c {
		t.Fatal("different keys should not share")
	}
	if got := h.created.Load(); got != 2 {
		t.Fatalf("created = %d, want 2", got)
	}
}

func TestPool_ClosesAfterLastReleaseAndIdle(t *testing.T) {
	h := newHarness(30 * time.Millisecond)
	a, ra := h.acquire(t, "k")
	_, rb := h.acquire(t, "k")

	ra()
	ra() // 重复 release 不应多减引用
	select {
	case <-h.closed:
		t.Fatal("must not close while another holder remains")
	case <-time.After(80 * time.Millisecond):
	}

	rb()
	select {
	case c := <-h.closed:
		if c != a {
			t.Fatal("closed unexpected client")
		}
	case <-time.After(time.Second):
		t.Fatal("client not closed after idle timeout")
	}
	if h.pool.Len() != 0 {
		t.Fatalf("pool len = %d, want 0", h.pool.Len())
	}

	// 关闭后再次 Acquire 会新建
	b, rb2 := h.acquire(t, "k")
	defer rb2()
	if b == a || h.created.Load() != 2 {
		t.Fatal("should create a fresh client after the old one was closed")
	}
}

func TestPool_ReacquireDuringIdleReuses(t *testing.T) {
	h := newHarness(100 * time.Millisecond)
	a, ra := h.acquire(t, "k")
	ra()

	b, rb := h.acquire(t, "k")
	if a != b {
		t.Fatal("reacquire within idle timeout should reuse the client")
	}
	select {
	case <-h.closed:
		t.Fatal("reused client must not be closed by the stale idle timer")
	case <-time.After(250 * time.Millisecond):
	}

	rb()
	select {
	case <-h.closed:
	case <-time.After(time.Second):
		t.Fatal("client not closed after final release")
	}
}

func TestPool_CreateErrorNotCached(t *testing.T) {
	h := newHarness(time.Hour)
	_, release, err := h.pool.Acquire("k", func() (*fakeClient, error) {
		return nil, errors.New("boom")
	})
	if err == nil {
		t.Fatal("want error")
	}
	release()
	if h.pool.Len() != 0 {
		t.Fatal("failed create must not be cached")
	}
	_, r := h.acquire(t, "k")
	r()
}

func TestPool_Concurrent(t *testing.T) {
	h := newHarness(5 * time.Millisecond)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				c, release, err := h.pool.Acquire("k", func() (*fakeClient, error) {
					return &fakeClient{id: int(h.created.Add(1))}, nil
				})
				if err != nil {
					t.Error(err)
					return
				}
				if c.closed.Load() {
					t.Error("acquired a closed client")
				}
				release()
			}
		}()
	}
	wg.Wait()

	// closeFn 在锁外执行，需等待关闭计数追上创建计数，而不是只看 Len()
	deadline := time.Now().Add(time.Second)
	for h.pool.Len() != 0 || h.closedN.Load() != h.created.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("pool not drained: len=%d closed=%d created=%d",
				h.pool.Len(), h.closedN.Load(), h.created.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
