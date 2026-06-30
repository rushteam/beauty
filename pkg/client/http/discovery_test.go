package resty_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	httpclient "github.com/rushteam/beauty/pkg/client/http"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

// mockDiscovery 实现 discover.Discovery,返回可控的服务列表。
type mockDiscovery struct {
	mu       sync.Mutex
	services []discover.ServiceInfo
	notify   discover.Notify
}

func (m *mockDiscovery) Find(ctx context.Context, name string) ([]discover.ServiceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]discover.ServiceInfo(nil), m.services...), nil
}

func (m *mockDiscovery) Watch(ctx context.Context, name string, n discover.Notify) error {
	m.mu.Lock()
	m.notify = n
	services := append([]discover.ServiceInfo(nil), m.services...)
	m.mu.Unlock()
	// 立即推送一次初始列表
	if n != nil && len(services) > 0 {
		n(services)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (m *mockDiscovery) update(services []discover.ServiceInfo) {
	m.mu.Lock()
	m.services = services
	notify := m.notify
	m.mu.Unlock()
	if notify != nil {
		notify(services)
	}
}

func newService(addr string, weight int, meta map[string]string) discover.ServiceInfo {
	if meta == nil {
		meta = map[string]string{}
	}
	if weight > 0 {
		meta["weight"] = fmt.Sprintf("%d", weight)
	}
	return discover.ServiceInfo{ID: addr, Addr: addr, Metadata: meta}
}

// newTestServer 起一个返回固定 ID 的 HTTP 后端,记录命中次数。
func newTestServer(id string, hits *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, id)
	}))
}

// ===== 选实例策略 =====

func TestSelectStrategy_RoundRobin(t *testing.T) {
	var a, b, c atomic.Int64
	srvA := newTestServer("a", &a)
	srvB := newTestServer("b", &b)
	srvC := newTestServer("c", &c)
	defer srvA.Close()
	defer srvB.Close()
	defer srvC.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil),
		newService(srvB.Listener.Addr().String(), 1, nil),
		newService(srvC.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := t.Context()
	for range 9 {
		resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	// 3 节点 × 3 轮 = 各 3 次
	for name, h := range map[string]*atomic.Int64{"a": &a, "b": &b, "c": &c} {
		if h.Load() != 3 {
			t.Errorf("%s want 3 hits, got %d", name, h.Load())
		}
	}
}

func TestSelectStrategy_WeightedRoundRobin(t *testing.T) {
	var a, b, c atomic.Int64
	srvA := newTestServer("a", &a)
	srvB := newTestServer("b", &b)
	srvC := newTestServer("c", &c)
	defer srvA.Close()
	defer srvB.Close()
	defer srvC.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil), // a=1
		newService(srvB.Listener.Addr().String(), 2, nil), // b=2
		newService(srvC.Listener.Addr().String(), 3, nil), // c=3
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPWeightedRoundRobin),
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := t.Context()
	for range 6 { // 一轮共 6 次
		resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	// SWRR 一轮精确分配:a=1 b=2 c=3
	if a.Load() != 1 || b.Load() != 2 || c.Load() != 3 {
		t.Errorf("one round want a=1 b=2 c=3, got a=%d b=%d c=%d", a.Load(), b.Load(), c.Load())
	}
}

func TestSelectStrategy_Random(t *testing.T) {
	var a, b atomic.Int64
	srvA := newTestServer("a", &a)
	srvB := newTestServer("b", &b)
	defer srvA.Close()
	defer srvB.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil),
		newService(srvB.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPRandom),
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := t.Context()
	for range 100 {
		resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	// 随机不应全打到一边
	if a.Load() == 0 || b.Load() == 0 {
		t.Errorf("random should hit both nodes, got a=%d b=%d", a.Load(), b.Load())
	}
}

// ===== NewRequest + transport 改写 URL =====

func TestNewRequest_TransportRewritesURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.URL.Path)
	}))
	defer srv.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srv.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := t.Context()
	// NewRequest 只设相对 path,host/scheme 由 transport 改写
	req, err := cli.NewRequest(ctx, http.MethodGet, "/api/users/123")
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if req.URL.Path != "/api/users/123" {
		t.Errorf("path want /api/users/123, got %s", req.URL.Path)
	}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "/api/users/123" {
		t.Errorf("body want /api/users/123, got %s", body)
	}
}

// ===== Do 灵活形式 =====

func TestDo_WithPrebuiltRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "yes")
		fmt.Fprint(w, "hello")
	}))
	defer srv.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srv.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := t.Context()
	req, _ := cli.NewRequest(ctx, http.MethodGet, "/hello")
	req.Header.Set("X-Req", "1")
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Custom") != "yes" {
		t.Error("missing response header")
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("body want hello, got %s", body)
	}
}

// ===== 重试换节点 =====

func TestRetry_DifferentNode(t *testing.T) {
	// 两个后端都返回 502,验证 5xx 触发重试且换节点(两节点都被命中)。
	var a, b atomic.Int64
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Add(1)
		http.Error(w, "bad gateway", http.StatusBadGateway) // 5xx 可重试
	}))
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.Add(1)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srvA.Close()
	defer srvB.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil),
		newService(srvB.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
		httpclient.WithHTTPMaxRetries(2),
		httpclient.WithHTTPRetryDelay(10*time.Millisecond),
		httpclient.WithHTTPRetryOnDifferentNode(true),
	)
	ctx := t.Context()
	resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
	// 重试 2 次仍 502:返回最后一次 resp(StatusCode=502),不带 error
	// (http.RoundTripper 契约:有 resp 时不返回 error,调用方自行判断状态码)
	if err != nil {
		t.Fatalf("expected resp not error after retries, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status want 502, got %d", resp.StatusCode)
	}
	// 3 次尝试(1+2重试),换节点应让两个节点都被命中
	if a.Load() == 0 || b.Load() == 0 {
		t.Errorf("retry on different node should hit both, got a=%d b=%d", a.Load(), b.Load())
	}
	total := a.Load() + b.Load()
	if total != 3 {
		t.Errorf("total attempts want 3, got %d", total)
	}
}

func TestRetry_NoDifferentNode(t *testing.T) {
	// 同节点重试:失败的节点被反复命中。
	var a atomic.Int64
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Add(1)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srvA.Close()

	// 只有一个节点,只能同节点重试
	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPMaxRetries(2),
		httpclient.WithHTTPRetryDelay(10*time.Millisecond),
		httpclient.WithHTTPRetryOnDifferentNode(false),
	)
	ctx := t.Context()
	resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
	// 502 重试 2 次后仍失败:返回 502 resp,无 error
	if err != nil {
		t.Fatalf("expected resp not error, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status want 502, got %d", resp.StatusCode)
	}
	// a 应被命中 3 次(1 首次 + 2 重试)
	if a.Load() != 3 {
		t.Errorf("same-node retry want 3 hits on a, got %d", a.Load())
	}
}

func TestRetry_NonRetryable_4xx(t *testing.T) {
	var a atomic.Int64
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Add(1)
		http.Error(w, "not found", http.StatusNotFound) // 4xx 不重试
	}))
	defer srvA.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPMaxRetries(3),
		httpclient.WithHTTPRetryDelay(10*time.Millisecond),
	)
	ctx := t.Context()
	resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
	// 4xx 不重试,但 resp 仍返回(由调用方判断状态码)
	if err != nil {
		t.Fatalf("4xx should return resp not error, got: %v", err)
	}
	resp.Body.Close()
	if a.Load() != 1 {
		t.Errorf("4xx should not retry, want 1 hit, got %d", a.Load())
	}
}

// ===== 标签过滤 =====

func TestLabelFilter_FailClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srv.Listener.Addr().String(), 1, map[string]string{"version": "v1"}),
	}}
	// 过滤 v2,无匹配
	f := selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2")
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPLabelFilter(f),
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := t.Context()
	_, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
	if err == nil {
		t.Fatal("fail-closed should error when no instance matches")
	}
}

func TestLabelFilter_Matches(t *testing.T) {
	var v1, v2 atomic.Int64
	srvV1 := newTestServer("v1", &v1)
	srvV2 := newTestServer("v2", &v2)
	defer srvV1.Close()
	defer srvV2.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvV1.Listener.Addr().String(), 1, map[string]string{"version": "v1"}),
		newService(srvV2.Listener.Addr().String(), 1, map[string]string{"version": "v2"}),
	}}
	f := selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2")
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPLabelFilter(f),
		httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := t.Context()
	for range 5 {
		resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if v1.Load() != 0 {
		t.Errorf("v1 should not be hit, got %d", v1.Load())
	}
	if v2.Load() != 5 {
		t.Errorf("v2 want 5 hits, got %d", v2.Load())
	}
}

// ===== 生命周期 =====

func TestStartStop_Idempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srv.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc")
	ctx := t.Context()
	// 多次 Start 幂等
	if err := cli.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := cli.Start(ctx); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	// 多次 Stop 幂等
	cli.Stop()
	cli.Stop()
}

func TestAutoStart_Works(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srv.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPMaxRetries(0),
	)
	// 不调用 Start,直接 DoWith 应触发 autoStart
	ctx := t.Context()
	resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("autoStart DoWith failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// ===== 并发安全 =====

func TestConcurrentSafe(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srv.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := t.Context()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 20 {
				resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
				if err != nil {
					t.Errorf("request failed: %v", err)
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		})
	}
	wg.Wait()
	if hits.Load() != 400 {
		t.Errorf("total hits want 400, got %d", hits.Load())
	}
}

// ===== 服务列表动态更新 =====

func TestWatch_UpdatesServiceList(t *testing.T) {
	var a, b atomic.Int64
	srvA := newTestServer("a", &a)
	srvB := newTestServer("b", &b)
	defer srvA.Close()
	defer srvB.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := cli.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// 等待 watch 初始推送
	time.Sleep(50 * time.Millisecond)

	// 初始只有 a
	resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if a.Load() != 1 || b.Load() != 0 {
		t.Fatalf("initial want a=1 b=0, got a=%d b=%d", a.Load(), b.Load())
	}

	// 动态加入 b
	disc.update([]discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil),
		newService(srvB.Listener.Addr().String(), 1, nil),
	})
	time.Sleep(50 * time.Millisecond)

	// 多次请求应命中 b
	for range 10 {
		resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if b.Load() == 0 {
		t.Error("after update, b should be hit")
	}
}
