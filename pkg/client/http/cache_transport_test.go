package resty_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	resty "github.com/rushteam/beauty/pkg/client/http"
)

func newCacheClient(store resty.HTTPCacheStore, opts ...resty.CacheTransportOption) *http.Client {
	return &http.Client{
		Transport: resty.NewCacheTransport(http.DefaultTransport, store, opts...),
	}
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// 第二次 GET 命中缓存,后端只被调用一次。
func TestCacheTransport_HitMiss(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))
	resp1, _ := c.Get(srv.URL)
	body1 := bodyString(t, resp1)

	resp2, _ := c.Get(srv.URL)
	body2 := bodyString(t, resp2)

	if body1 != "hello" || body2 != "hello" {
		t.Fatalf("body = %q / %q, want hello", body1, body2)
	}
	if hits.Load() != 1 {
		t.Fatalf("后端命中 %d 次, want 1", hits.Load())
	}

	ct := c.Transport.(*resty.CacheTransport)
	stats := ct.Stats()
	if stats.Hits != 1 || stats.Misses != 1 {
		t.Fatalf("stats = %+v, want Hits=1 Misses=1", stats)
	}
}

// POST 默认不缓存。
func TestCacheTransport_SkipNonCacheableMethod(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))
	c.Post(srv.URL, "text/plain", nil)
	c.Post(srv.URL, "text/plain", nil)
	if hits.Load() != 2 {
		t.Fatalf("POST 不应缓存, 后端命中 %d 次 want 2", hits.Load())
	}
}

// Cache-Control: no-store 的响应不被缓存。
func TestCacheTransport_RespNoStore(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte("secret"))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))
	c.Get(srv.URL)
	c.Get(srv.URL)
	if hits.Load() != 2 {
		t.Fatalf("no-store 响应不应缓存, 后端命中 %d 次 want 2", hits.Load())
	}
}

// Cache-Control: private 的响应不被缓存。
func TestCacheTransport_RespPrivate(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Write([]byte("private"))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))
	c.Get(srv.URL)
	c.Get(srv.URL)
	if hits.Load() != 2 {
		t.Fatalf("private 响应不应缓存, 后端命中 %d 次 want 2", hits.Load())
	}
}

// 请求头 Cache-Control: no-store 绕过缓存。
func TestCacheTransport_ReqNoStore(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Cache-Control", "no-store")
	c.Do(req)
	c.Do(req)
	if hits.Load() != 2 {
		t.Fatalf("请求 no-store 应绕过缓存, 后端命中 %d 次 want 2", hits.Load())
	}
}

// max-age 过期后重新获取。
func TestCacheTransport_MaxAgeExpiry(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=0")
		w.Write([]byte("v" + strconv.FormatInt(n, 10)))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))
	c.Get(srv.URL)
	time.Sleep(10 * time.Millisecond)
	c.Get(srv.URL)
	if hits.Load() != 2 {
		t.Fatalf("max-age=0 过期后应重新获取, 后端命中 %d 次 want 2", hits.Load())
	}
}

// ForceTTL 忽略服务端 Cache-Control。
func TestCacheTransport_ForceTTL(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "no-store, private")
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheForceTTL(),
		resty.WithCacheDefaultTTL(time.Minute),
	)
	c.Get(srv.URL)
	c.Get(srv.URL)
	if hits.Load() != 1 {
		t.Fatalf("forceTTL 应忽略 no-store/private, 后端命中 %d 次 want 1", hits.Load())
	}
}

// 条件请求:ETag → If-None-Match → 304 命中。
func TestCacheTransport_ConditionalETag(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Cache-Control", "max-age=0")
		w.Header().Set("ETag", `"abc"`)
		w.Write([]byte("fresh"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheConditionalRequest(),
	)

	resp1, _ := c.Get(srv.URL)
	b1 := bodyString(t, resp1)
	if b1 != "fresh" {
		t.Fatalf("first body = %q, want fresh", b1)
	}

	time.Sleep(5 * time.Millisecond)

	resp2, _ := c.Get(srv.URL)
	b2 := bodyString(t, resp2)
	if b2 != "fresh" {
		t.Fatalf("conditional body = %q, want fresh (from cache)", b2)
	}
	if hits.Load() != 2 {
		t.Fatalf("后端命中 %d 次, want 2 (1 origin + 1 conditional)", hits.Load())
	}
	if resp2.StatusCode != 200 {
		t.Fatalf("conditional 应返回 200(缓存体), got %d", resp2.StatusCode)
	}
}

// 条件请求:Last-Modified → If-Modified-Since → 304。
func TestCacheTransport_ConditionalLastModified(t *testing.T) {
	lm := "Wed, 30 Jul 2026 00:00:00 GMT"
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-Modified-Since") == lm {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Cache-Control", "max-age=0")
		w.Header().Set("Last-Modified", lm)
		w.Write([]byte("page"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheConditionalRequest(),
	)
	c.Get(srv.URL)
	time.Sleep(5 * time.Millisecond)
	resp, _ := c.Get(srv.URL)
	body := bodyString(t, resp)
	if body != "page" {
		t.Fatalf("conditional body = %q, want page", body)
	}
	if hits.Load() != 2 {
		t.Fatalf("后端命中 %d 次, want 2", hits.Load())
	}
}

// Singleflight:并发 GET 同一 URL 只打一次后端。
func TestCacheTransport_SingleFlight(t *testing.T) {
	var hits atomic.Int64
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		close(started)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Write([]byte("shared"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheSingleFlight(),
	)

	const n = 10
	var wg sync.WaitGroup
	bodies := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := c.Get(srv.URL)
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				return
			}
			bodies[idx] = bodyString(t, resp)
		}(i)
	}
	wg.Wait()

	if hits.Load() != 1 {
		t.Fatalf("singleflight 下后端应只命中 1 次, got %d", hits.Load())
	}
	for i, b := range bodies {
		if b != "shared" {
			t.Fatalf("goroutine %d body = %q, want shared", i, b)
		}
	}
}

// 自定义 key 函数。
func TestCacheTransport_CustomKeyFunc(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheKeyFunc(func(r *http.Request) string {
			return "static-key"
		}),
	)
	c.Get(srv.URL + "/a")
	c.Get(srv.URL + "/b")
	if hits.Load() != 1 {
		t.Fatalf("相同 key 应命中缓存, 后端命中 %d 次 want 1", hits.Load())
	}
}

// 自定义 filter 排除特定请求。
func TestCacheTransport_CustomFilter(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheFilter(func(r *http.Request) bool {
			return r.URL.Query().Get("cache") != "false"
		}),
	)
	c.Get(srv.URL + "?cache=false")
	c.Get(srv.URL + "?cache=false")
	if hits.Load() != 2 {
		t.Fatalf("filter 排除后不应缓存, 后端命中 %d 次 want 2", hits.Load())
	}
}

// 4xx/5xx 不被缓存。
func TestCacheTransport_ErrorStatusNotCached(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("err"))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))
	c.Get(srv.URL)
	c.Get(srv.URL)
	if hits.Load() != 2 {
		t.Fatalf("5xx 不应缓存, 后端命中 %d 次 want 2", hits.Load())
	}
}

// Vary: * 不可缓存。
func TestCacheTransport_VaryStar(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Vary", "*")
		w.Write([]byte("vary"))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))
	c.Get(srv.URL)
	c.Get(srv.URL)
	if hits.Load() != 2 {
		t.Fatalf("Vary:* 不应缓存, 后端命中 %d 次 want 2", hits.Load())
	}
}

// WithCacheMethods 允许缓存 HEAD。
func TestCacheTransport_CacheMethods(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheMethods("GET", "HEAD"),
	)
	req1, _ := http.NewRequest("HEAD", srv.URL, nil)
	c.Do(req1)
	req2, _ := http.NewRequest("HEAD", srv.URL, nil)
	c.Do(req2)
	if hits.Load() != 1 {
		t.Fatalf("HEAD 应可缓存, 后端命中 %d 次 want 1", hits.Load())
	}
}

// MemoryHTTPCache 容量满时淘汰。
func TestMemoryHTTPCache_Eviction(t *testing.T) {
	m := resty.NewMemoryHTTPCache(2)
	r := &resty.CachedResponse{StatusCode: 200, Header: http.Header{}, Body: nil, StoredAt: time.Now()}

	m.Set("a", r, time.Minute)
	m.Set("b", r, time.Minute)
	m.Set("c", r, time.Minute)
	if m.Len() != 2 {
		t.Fatalf("len = %d, want 2 (容量 2,应淘汰一条)", m.Len())
	}
}

// MemoryHTTPCache TTL 过期。
func TestMemoryHTTPCache_TTLExpiry(t *testing.T) {
	m := resty.NewMemoryHTTPCache(16)
	r := &resty.CachedResponse{StatusCode: 200, Header: http.Header{}, Body: nil, StoredAt: time.Now()}

	m.Set("k", r, 10*time.Millisecond)
	if _, ok := m.Get("k"); !ok {
		t.Fatal("should hit before expiry")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := m.Get("k"); ok {
		t.Fatal("should miss after expiry")
	}
}

// MemoryHTTPCache Delete。
func TestMemoryHTTPCache_Delete(t *testing.T) {
	m := resty.NewMemoryHTTPCache(16)
	r := &resty.CachedResponse{StatusCode: 200, Header: http.Header{}, Body: nil, StoredAt: time.Now()}
	m.Set("x", r, time.Minute)
	m.Delete("x")
	if _, ok := m.Get("x"); ok {
		t.Fatal("should be deleted")
	}
}

// DefaultTTL:无 Cache-Control 时使用 defaultTTL。
func TestCacheTransport_DefaultTTLUsed(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte("no-cc"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheDefaultTTL(time.Minute),
	)
	c.Get(srv.URL)
	c.Get(srv.URL)
	if hits.Load() != 1 {
		t.Fatalf("无 CC 应使用 defaultTTL 缓存, 后端命中 %d 次 want 1", hits.Load())
	}
}

// 请求 Cache-Control: no-cache 强制重验(无条件请求时变成 miss)。
func TestCacheTransport_ReqNoCache(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	c := newCacheClient(resty.NewMemoryHTTPCache(16))
	c.Get(srv.URL)

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Cache-Control", "no-cache")
	c.Do(req)

	if hits.Load() != 2 {
		t.Fatalf("no-cache 请求应强制重验, 后端命中 %d 次 want 2", hits.Load())
	}
}

// IgnoreRequestDirectives:请求带 no-store/no-cache 仍然走缓存,但仍遵守响应端 CC。
func TestCacheTransport_IgnoreRequestDirectives(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheIgnoreRequestDirectives(),
	)

	// 首次请求正常缓存
	c.Get(srv.URL)

	// 带 no-store 的请求仍命中缓存
	req1, _ := http.NewRequest("GET", srv.URL, nil)
	req1.Header.Set("Cache-Control", "no-store")
	c.Do(req1)

	// 带 no-cache 的请求仍命中缓存
	req2, _ := http.NewRequest("GET", srv.URL, nil)
	req2.Header.Set("Cache-Control", "no-cache")
	c.Do(req2)

	if hits.Load() != 1 {
		t.Fatalf("ignoreReqCC 下请求端 CC 应被忽略, 后端命中 %d 次 want 1", hits.Load())
	}
}

// IgnoreRequestDirectives 仍然遵守响应端 no-store。
func TestCacheTransport_IgnoreRequestDirectives_RespectResponseCC(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte("secret"))
	}))
	defer srv.Close()

	c := newCacheClient(
		resty.NewMemoryHTTPCache(16),
		resty.WithCacheIgnoreRequestDirectives(),
	)
	c.Get(srv.URL)
	c.Get(srv.URL)

	if hits.Load() != 2 {
		t.Fatalf("响应端 no-store 仍应生效, 后端命中 %d 次 want 2", hits.Load())
	}
}
