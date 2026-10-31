package bulkhead

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	rbulkhead "github.com/rushteam/beauty/pkg/resilience/bulkhead"
)

func TestHTTPMiddlewareRejectsWhenFull(t *testing.T) {
	bh := rbulkhead.New(1) // 并发 1,无队列
	release := make(chan struct{})
	h := HTTPMiddleware(bh, Config{RetryAfterSeconds: 2})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // 占住槽位
		w.WriteHeader(http.StatusOK)
	}))

	// 第一个请求占住唯一槽位。
	firstDone := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		firstDone <- rec.Code
	}()
	time.Sleep(20 * time.Millisecond)

	// 第二个请求应被立即拒绝 503。
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/", nil))
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request code=%d want 503", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After=%q", rec2.Header().Get("Retry-After"))
	}

	close(release)
	if code := <-firstDone; code != http.StatusOK {
		t.Fatalf("first request code=%d want 200", code)
	}
}

func TestHTTPMiddlewareAllowsUpToLimit(t *testing.T) {
	bh := rbulkhead.New(3, rbulkhead.WithQueue(100))
	var mu sync.Mutex
	var peak, cur int
	h := HTTPMiddleware(bh)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cur++
		if cur > peak {
			peak = cur
		}
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		cur--
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("code=%d", rec.Code)
			}
		}()
	}
	wg.Wait()
	if peak > 3 {
		t.Fatalf("peak concurrency %d exceeded 3", peak)
	}
}

func TestHTTPMiddlewareCustomOnRejected(t *testing.T) {
	bh := rbulkhead.New(1)
	release := make(chan struct{})
	called := false
	h := HTTPMiddleware(bh, Config{
		OnRejected: func(w http.ResponseWriter, r *http.Request, err error) {
			called = true
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))

	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	}()
	time.Sleep(20 * time.Millisecond)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !called || rec.Code != http.StatusTooManyRequests {
		t.Fatalf("custom OnRejected not used: called=%v code=%d", called, rec.Code)
	}
	close(release)
}
