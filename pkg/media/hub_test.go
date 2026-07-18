package media_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/hls"
	"github.com/rushteam/beauty/pkg/media"
)

func newStreamHub() *media.Hub[*hls.Stream] {
	return media.NewHub(func(string) *hls.Stream { return hls.NewStream() })
}

func TestHub_AcquireRejectRelease(t *testing.T) {
	h := newStreamHub()

	s1, ok := h.Acquire("live1")
	if !ok || s1 == nil {
		t.Fatal("首次 Acquire 应成功")
	}
	if h.Count() != 1 {
		t.Fatalf("count = %d, want 1", h.Count())
	}
	// 重复 key → 拒绝(防抢流)
	if _, ok := h.Acquire("live1"); ok {
		t.Fatal("重复推流应被拒绝")
	}

	// 另一路
	if _, ok := h.Acquire("live2"); !ok {
		t.Fatal("不同 key 应成功")
	}
	if h.Count() != 2 {
		t.Fatalf("count = %d, want 2", h.Count())
	}

	// Session.Context 在 Release 时取消
	ctx := s1.Context()
	h.Release("live1")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Release 后 Session.Context 应取消")
	}
	if h.Count() != 1 {
		t.Fatalf("count after release = %d, want 1", h.Count())
	}
	// Release 幂等
	h.Release("live1")
	h.Release("nonexistent")
}

func TestHub_Routing(t *testing.T) {
	h := media.NewHub(func(key string) *hls.Stream {
		return hls.NewStream(hls.WithWindow(4))
	})
	sess, _ := h.Acquire("room42")
	sess.Stream.Append([]byte("TSDATA"), time.Second)

	srv := httptest.NewServer(http.StripPrefix("/live", h))
	defer srv.Close()

	// 路由到该流的分片
	resp, err := http.Get(srv.URL + "/live/room42/seg0.ts")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	n, _ := resp.Body.Read(buf)
	resp.Body.Close()
	if string(buf[:n]) != "TSDATA" {
		t.Fatalf("seg body = %q", buf[:n])
	}

	// 未知流 → 404
	resp, _ = http.Get(srv.URL + "/live/ghost/index.m3u8")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知流应 404, got %d", resp.StatusCode)
	}
}

func TestHub_ConcurrentAcquire(t *testing.T) {
	h := newStreamHub()
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 20 {
		wg.Go(func() {
			if _, ok := h.Acquire("same"); ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("并发抢同一 key 只应 1 个成功, got %d", wins)
	}
	if h.Count() != 1 {
		t.Fatalf("count = %d, want 1", h.Count())
	}
}
