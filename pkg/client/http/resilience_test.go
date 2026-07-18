package resty_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
	resty "github.com/rushteam/beauty/pkg/client/http"
	mwcb "github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
)

func fastPolicy(retries int) *backoff.Policy {
	return backoff.New(backoff.WithMaxRetries(retries), backoff.WithBase(time.Millisecond))
}

// 幂等请求遇 503 重试,最终成功。
func TestWithRetry_RetriesOn503(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := resty.NewHTTPClient(resty.WithRetry(fastPolicy(5)))
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hits.Load() != 3 {
		t.Fatalf("命中 %d 次, want 3(2 次 503 + 1 次成功)", hits.Load())
	}
}

// 成功响应不重试。
func TestWithRetry_NoRetryOnSuccess(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := resty.NewHTTPClient(resty.WithRetry(fastPolicy(3)))
	resp, _ := c.Get(srv.URL)
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("命中 %d 次, want 1", hits.Load())
	}
}

// 非幂等 POST 默认不重试。
func TestWithRetry_POSTNotRetried(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := resty.NewHTTPClient(resty.WithRetry(fastPolicy(3)))
	resp, err := c.Post(srv.URL, "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("POST 不应重试, 命中 %d 次 want 1", hits.Load())
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("应原样返回 503, got %d", resp.StatusCode)
	}
}

// 幂等 PUT 带请求体:重试时请求体正确重放。
func TestWithRetry_BodyReplay(t *testing.T) {
	var hits atomic.Int64
	var lastBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody.Store(string(b))
		if hits.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := resty.NewHTTPClient(resty.WithRetry(fastPolicy(3)))
	req, _ := http.NewRequest(http.MethodPut, srv.URL, strings.NewReader("hello-body"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 2 {
		t.Fatalf("命中 %d 次, want 2", hits.Load())
	}
	if got := lastBody.Load().(string); got != "hello-body" {
		t.Fatalf("重放后请求体 = %q, want hello-body", got)
	}
}

// 退避等待期间 ctx 取消,立即返回。
func TestWithRetry_ContextCancel(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	// base 5s:第一次 503 后进入长等待,被 ctx 取消打断。
	policy := backoff.New(backoff.WithMaxRetries(3), backoff.WithBase(5*time.Second))
	c := resty.NewHTTPClient(resty.WithRetry(policy))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	start := time.Now()
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("应返回 ctx 取消错误")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("应被 ctx 快速打断, 耗时 %v", time.Since(start))
	}
	if hits.Load() != 1 {
		t.Fatalf("只应打一次, 命中 %d", hits.Load())
	}
}

// 连续失败触发熔断打开,后续请求短路(不再打后端)。
func TestWithCircuitBreaker_Opens(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cb := mwcb.NewCircuitBreaker(mwcb.ConsecutiveFailuresConfig("test-http", 2))
	c := resty.NewHTTPClient(resty.WithCircuitBreaker(cb))

	const n = 8
	var openErrs int
	for range n {
		resp, err := c.Get(srv.URL)
		if err != nil {
			openErrs++
			continue
		}
		resp.Body.Close()
	}
	if hits.Load() >= n {
		t.Fatalf("熔断应短路部分请求, 后端命中 %d/%d", hits.Load(), n)
	}
	if openErrs == 0 {
		t.Fatal("熔断打开后应有请求返回错误(短路)")
	}
}
