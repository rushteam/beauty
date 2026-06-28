package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/ratelimit"
)

func TestTokenBucket_AllowsBurst(t *testing.T) {
	tb := ratelimit.NewTokenBucket(5, 1) // 突发5,1/s
	defer tb.Stop()
	allowed := 0
	for range 10 {
		if ok, _ := tb.Allow("u1"); ok {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("burst want 5, got %d", allowed)
	}
}

func TestTokenBucket_Refills(t *testing.T) {
	tb := ratelimit.NewTokenBucket(1, 100) // 1桶,100/s
	defer tb.Stop()
	if ok, _ := tb.Allow("u1"); !ok {
		t.Fatal("first allow should pass")
	}
	if ok, _ := tb.Allow("u1"); ok {
		t.Fatal("second immediate allow should fail")
	}
	time.Sleep(20 * time.Millisecond) // 等补令牌(100/s → 10ms 补1个)
	if ok, _ := tb.Allow("u1"); !ok {
		t.Fatal("should allow after refill")
	}
}

func TestTokenBucket_RetryAfter(t *testing.T) {
	tb := ratelimit.NewTokenBucket(1, 1) // 1桶,1/s
	defer tb.Stop()
	tb.Allow("u1")
	_, retry := tb.Allow("u1")
	if retry <= 0 {
		t.Fatalf("retryAfter should be >0 when limited, got %v", retry)
	}
	if retry > time.Second {
		t.Fatalf("retryAfter should be <=1s, got %v", retry)
	}
}

func TestTokenBucket_PerKeyIsolated(t *testing.T) {
	tb := ratelimit.NewTokenBucket(1, 1)
	defer tb.Stop()
	if ok, _ := tb.Allow("a"); !ok {
		t.Fatal("a should pass")
	}
	if ok, _ := tb.Allow("b"); !ok {
		t.Fatal("b should be independent of a")
	}
}

func TestTokenBucket_Unlimited(t *testing.T) {
	tb := ratelimit.NewTokenBucket(0, 0)
	defer tb.Stop()
	for range 100 {
		if ok, _ := tb.Allow("k"); !ok {
			t.Fatal("unlimited should always allow")
		}
	}
}

func TestSlidingWindow_LimitsInWindow(t *testing.T) {
	sw := ratelimit.NewSlidingWindow(3, 100*time.Millisecond)
	defer sw.Stop()
	for i := range 5 {
		ok, _ := sw.Allow("u1")
		want := i < 3
		if ok != want {
			t.Fatalf("call %d: ok=%v want %v", i, ok, want)
		}
	}
}

func TestSlidingWindow_SlidesAfterWindow(t *testing.T) {
	sw := ratelimit.NewSlidingWindow(1, 50*time.Millisecond)
	defer sw.Stop()
	if ok, _ := sw.Allow("u1"); !ok {
		t.Fatal("first should pass")
	}
	if ok, _ := sw.Allow("u1"); ok {
		t.Fatal("second should fail")
	}
	time.Sleep(60 * time.Millisecond)
	if ok, _ := sw.Allow("u1"); !ok {
		t.Fatal("should pass after window slides")
	}
}

func TestSlidingWindow_RetryAfter(t *testing.T) {
	sw := ratelimit.NewSlidingWindow(1, 100*time.Millisecond)
	defer sw.Stop()
	sw.Allow("u1")
	_, retry := sw.Allow("u1")
	if retry <= 0 || retry > 100*time.Millisecond {
		t.Fatalf("retry want (0,100ms], got %v", retry)
	}
}

func TestSlidingWindow_PerKeyIsolated(t *testing.T) {
	sw := ratelimit.NewSlidingWindow(1, time.Second)
	defer sw.Stop()
	sw.Allow("a")
	if ok, _ := sw.Allow("b"); !ok {
		t.Fatal("b independent of a")
	}
}

func TestMiddleware_429OnLimit(t *testing.T) {
	tb := ratelimit.NewTokenBucket(1, 0.0001) // 基本用完不补
	defer tb.Stop()
	h := ratelimit.Middleware(tb, ratelimit.ClientIP)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	// 第1次放行。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("first: code=%d", rec.Code)
	}
	// 第2次限流。
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second: code=%d want 429", rec2.Code)
	}
	if ra := rec2.Header().Get("Retry-After"); ra == "" {
		t.Fatal("Retry-After header should be set")
	}
}

func TestMiddleware_EmptyKeySkips(t *testing.T) {
	tb := ratelimit.NewTokenBucket(1, 1)
	defer tb.Stop()
	called := atomic.Bool{}
	h := ratelimit.Middleware(tb, func(*http.Request) string { return "" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.Write([]byte("ok"))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	if !called.Load() {
		t.Fatal("handler should be called when key empty")
	}
}

func TestClientIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 10.0.0.1")
	if got := ratelimit.ClientIP(req); got != "9.9.9.9" {
		t.Fatalf("want 9.9.9.9, got %s", got)
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	if got := ratelimit.ClientIP(req); got != "1.2.3.4" {
		t.Fatalf("want 1.2.3.4, got %s", got)
	}
}

func TestTokenBucket_Concurrent(t *testing.T) {
	tb := ratelimit.NewTokenBucket(100, 1000)
	defer tb.Stop()
	var allowed atomic.Int64
	for range 200 {
		go func() {
			if ok, _ := tb.Allow("k"); ok {
				allowed.Add(1)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	got := allowed.Load()
	// 200 并发请求,桶只有100,补的来不及。放行数应 <= 100 + 一点补充。
	if got > 120 {
		t.Fatalf("concurrent allowed %d, should be <= ~100+refill", got)
	}
}

func TestTokenBucket_Stop_Idempotent(t *testing.T) {
	tb := ratelimit.NewTokenBucket(1, 1)
	tb.Stop()
	tb.Stop()
}

func TestSlidingWindow_Stop_Idempotent(t *testing.T) {
	sw := ratelimit.NewSlidingWindow(1, time.Second)
	sw.Stop()
	sw.Stop()
}
