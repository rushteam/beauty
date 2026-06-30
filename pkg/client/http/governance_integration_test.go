package resty_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	httpclient "github.com/rushteam/beauty/pkg/client/http"
	governancecb "github.com/rushteam/beauty/pkg/governance/circuitbreaker"
	governancerouter "github.com/rushteam/beauty/pkg/governance/router"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

// 验证端到端治理链路:3 节点(1 个持续 502),熔断器跳过故障节点、
// bannednodes 重试不重复选、router 按 version 过滤。
func TestGovernanceIntegration_CircuitBreakerSkipsBadNode(t *testing.T) {
	var good, bad atomic.Int64
	srvGood := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		good.Add(1)
		w.WriteHeader(200)
	}))
	srvBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		bad.Add(1)
		w.WriteHeader(502) // 持续 5xx
	}))
	defer srvGood.Close()
	defer srvBad.Close()

	// 熔断器:1 次失败即熔断该节点,冷却 1s
	breaker := governancecb.NewNodeBreaker(
		governancecb.WithFailureThreshold(1),
		governancecb.WithTimeout(time.Second),
	)
	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvGood.Listener.Addr().String(), 1, nil),
		newService(srvBad.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
		httpclient.WithHTTPMaxRetries(0), // 不靠 client 重试,靠熔断器跳过
		httpclient.WithHTTPCircuitBreaker(breaker),
	)
	ctx := t.Context()

	// 持续请求:bad 节点被熔断后,所有请求应走 good
	for range 20 {
		resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
	}
	// bad 最多被命中几次(熔断前),之后全走 good
	if bad.Load() > 5 {
		t.Errorf("bad node should be circuit-broken, got %d hits (>5)", bad.Load())
	}
	if good.Load() < 15 {
		t.Errorf("good node should receive most traffic, got %d (<15)", good.Load())
	}
}

func TestGovernanceIntegration_BannedNodesNoRepeat(t *testing.T) {
	// 两节点都返回 502,重试换节点时 bannednodes 保证不重复选同一节点
	var a, b atomic.Int64
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		a.Add(1)
		w.WriteHeader(502)
	}))
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b.Add(1)
		w.WriteHeader(502)
	}))
	defer srvA.Close()
	defer srvB.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvA.Listener.Addr().String(), 1, nil),
		newService(srvB.Listener.Addr().String(), 1, nil),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
		httpclient.WithHTTPMaxRetries(1), // 总尝试 2 次
		httpclient.WithHTTPRetryDelay(10*time.Millisecond),
		httpclient.WithHTTPRetryOnDifferentNode(true),
	)
	ctx := t.Context()
	resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("expected resp, got err: %v", err)
	}
	resp.Body.Close()
	// 2 次尝试应分别命中 a 和 b(bannednodes 保证不重复)
	total := a.Load() + b.Load()
	if total != 2 {
		t.Errorf("want 2 total attempts, got %d", total)
	}
	if a.Load() == 0 || b.Load() == 0 {
		t.Errorf("bannednodes should ensure both nodes hit once, got a=%d b=%d", a.Load(), b.Load())
	}
}

func TestGovernanceIntegration_RouterFiltersByVersion(t *testing.T) {
	var v1, v2 atomic.Int64
	srvV1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		v1.Add(1)
		w.WriteHeader(200)
	}))
	srvV2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		v2.Add(1)
		w.WriteHeader(200)
	}))
	defer srvV1.Close()
	defer srvV2.Close()

	// router 只放行 v2
	router := governancerouter.NewLabelRouter(
		selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2"),
	)
	disc := &mockDiscovery{services: []discover.ServiceInfo{
		newService(srvV1.Listener.Addr().String(), 1, map[string]string{"version": "v1"}),
		newService(srvV2.Listener.Addr().String(), 1, map[string]string{"version": "v2"}),
	}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
		httpclient.WithHTTPMaxRetries(0),
		httpclient.WithHTTPServiceRouter(router),
	)
	ctx := t.Context()
	for range 10 {
		resp, err := cli.DoWith(ctx, http.MethodGet, "/", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
	}
	if v1.Load() != 0 {
		t.Errorf("v1 should not be hit (router filters it out), got %d", v1.Load())
	}
	if v2.Load() != 10 {
		t.Errorf("v2 should get all 10 requests, got %d", v2.Load())
	}
}
