package resty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"log/slog"

	"github.com/rushteam/beauty/pkg/governance/bannednodes"
	governancecb "github.com/rushteam/beauty/pkg/governance/circuitbreaker"
	governancerouter "github.com/rushteam/beauty/pkg/governance/router"
	"github.com/rushteam/beauty/pkg/loadbalance"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// discoveryTransport 实现 http.RoundTripper,把"服务名 + 相对路径"透明地
// 路由到从服务发现选出的实例。请求的 URL.Host 被改写为选中实例的地址,
// 其余部分(method/header/body/ctx)保持不变。底层用 base RoundTripper
// 发实际请求(默认 otelhttp 包装的 http.DefaultTransport)。
//
// 重试:网络错误或 5xx 触发重试,指数退避 + ±25% jitter;retryOnDiffNode=true
// 时每次重试重新选实例。context 取消/超时与 4xx 不重试。
//
// 带可重放 body 的请求(PUT/POST)需设置 req.GetBody,否则首次读取后会被缓存
// 以支持重试(小 body 场景);大 body 流建议调用方提供 GetBody。
type discoveryTransport struct {
	discoveryConfig
	base http.RoundTripper

	mu       sync.RWMutex
	services []discover.ServiceInfo

	rr  *loadbalance.RoundRobin[httpServiceNode]
	wrr *loadbalance.WeightedRoundRobin[httpServiceNode]

	// 后台 goroutine 生命周期
	startOnce sync.Once
	stopOnce  sync.Once
	stopFn    context.CancelFunc
	bgWg      sync.WaitGroup
}

// discoveryConfig 是 transport 与 client 包装层共享的配置。
// HTTPDiscoveryOption 作用于此结构,使同一组 Option 既可配 transport 也可配 client。
type discoveryConfig struct {
	discovery       discover.Discovery
	serviceName     string
	strategy        HTTPBalanceStrategy
	filter          *selector.LabelFilter
	maxRetries      int
	retryDelay      time.Duration
	retryOnDiffNode bool
	timeout         time.Duration // http.Client 超时(仅 client 层生效)
	// 服务治理:节点级熔断 + 路由过滤。默认 NoopBreaker/NoopRouter,零开销。
	breaker governancecb.CircuitBreaker
	router  governancerouter.ServiceRouter
}

// NewDiscoveryTransport 创建服务发现 RoundTripper。
// base 为 nil 时用 otelhttp 包装的 http.DefaultTransport。
// 返回的 RoundTripper 可直接塞进 http.Client.Transport,或用 NewServiceDiscoveryHTTPClient 包装。
func NewDiscoveryTransport(discovery discover.Discovery, serviceName string, opts ...HTTPDiscoveryOption) http.RoundTripper {
	cfg := discoveryConfig{
		discovery:       discovery,
		serviceName:     serviceName,
		maxRetries:      1,
		retryDelay:      time.Second,
		retryOnDiffNode: true,
		breaker:         governancecb.NoopBreaker{},
		router:          governancerouter.NoopRouter{},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	t := &discoveryTransport{
		discoveryConfig: cfg,
		base:            newDefaultTransport(),
		rr:              loadbalance.NewRoundRobin[httpServiceNode](nil),
		wrr:             loadbalance.NewWeightedRoundRobin[httpServiceNode](nil),
	}
	return t
}

// RoundTrip 实现 http.RoundTripper。改写 URL.Host 后转发给 base。
func (t *discoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	t.autoStart()

	// 注入 bannednodes(若调用方未注入),供重试链内 Ban 失败节点用
	if !bannednodes.IsInjected(ctx) {
		ctx = bannednodes.WithBannedNodes(ctx)
		req = req.WithContext(ctx)
	}

	origPath := req.URL.Path
	origMethod := req.Method

	// 提取可重放 body:优先 GetBody,否则读取并缓存(支持无 GetBody 的调用方)
	var getBody func() (io.ReadCloser, error)
	if req.GetBody != nil {
		getBody = req.GetBody
	} else if req.Body != nil {
		bodyBytes, rerr := io.ReadAll(req.Body)
		req.Body.Close()
		if rerr != nil {
			return nil, fmt.Errorf("read request body: %w", rerr)
		}
		getBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(bodyBytes)), nil }
	}

	attempts := t.maxRetries + 1
	var lastErr error
	var lastResp *http.Response
	var firstReq *http.Request // 同节点重试时沿用
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			if lastResp != nil {
				lastResp.Body.Close()
			}
			return nil, err
		}
		curReq, err := t.buildReq(ctx, req, origMethod, origPath, getBody, firstReq)
		if err != nil {
			lastErr = err
			if i == 0 {
				return nil, err // 首次选实例失败直接返回(无节点)
			}
			continue
		}
		if i == 0 {
			firstReq = curReq
		}
		// 选中节点地址(供失败时 Ban + Report)
		nodeAddr := curReq.Host
		nodeInfo := &discover.ServiceInfo{Addr: nodeAddr}
		start := time.Now()
		resp, err := t.base.RoundTrip(curReq)
		if err == nil && !shouldRetryStatus(resp.StatusCode) {
			t.breaker.Report(nodeInfo, time.Since(start), nil)
			return resp, nil
		}
		if resp != nil {
			lastResp = resp
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("server error: %s", resp.Status)
		}
		// 失败反馈:ban 本次请求 + 熔断器记录
		if nodeAddr != "" {
			bannednodes.Ban(ctx, nodeAddr)
			t.breaker.Report(nodeInfo, time.Since(start), lastErr)
		}
		if isHTTPNonRetryable(err, resp) {
			// 4xx:返回 resp 不带 error(调用方判断状态码);纯 ctx 错误返回 error。
			if resp != nil {
				return resp, nil
			}
			return nil, lastErr
		}
		if resp != nil && i < attempts-1 {
			resp.Body.Close()
		}
		if i < attempts-1 {
			base := t.retryDelay * (1 << i)
			jitter := time.Duration(rand.Int64N(int64(base/2))) - base/4
			delay := base + jitter
			select {
			case <-ctx.Done():
				if lastResp != nil {
					lastResp.Body.Close()
				}
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	// 重试耗尽:若最后一次拿到了 resp(如 502),返回它(不带 error——
	// http.RoundTripper 契约禁止同时返回 resp 和 error,http.Client 会丢弃 resp)。
	// 调用方自行判断 resp.StatusCode。只有纯网络错误(无 resp)才返回 error。
	if lastResp != nil {
		return lastResp, nil
	}
	return nil, fmt.Errorf("failed after %d retries: %w", t.maxRetries, lastErr)
}

// buildReq 为第 i 次尝试构造请求。
//   - i==0:选实例 + Clone orig,改写 URL,记为 firstReq
//   - i>0 且 retryOnDiffNode:重新选实例 + Clone orig
//   - i>0 且 !retryOnDiffNode:沿用 firstReq,只重放 body
func (t *discoveryTransport) buildReq(ctx context.Context, orig *http.Request, method, path string, getBody func() (io.ReadCloser, error), firstReq *http.Request) (*http.Request, error) {
	// 同节点重试:沿用 firstReq,重放 body
	if firstReq != nil && !t.retryOnDiffNode {
		if getBody != nil {
			body, err := getBody()
			if err != nil {
				return nil, err
			}
			firstReq.Body = body
		}
		return firstReq, nil
	}
	// 选实例
	node := t.selectService(ctx, t.snapshot())
	if node == nil {
		return nil, fmt.Errorf("no suitable instance for service %s", t.serviceName)
	}
	curReq := orig.Clone(ctx)
	curReq.URL.Scheme = node.scheme
	curReq.URL.Host = node.service.Addr
	curReq.Host = node.service.Addr
	curReq.URL.Path = path
	curReq.Method = method
	if getBody != nil {
		body, err := getBody()
		if err != nil {
			return nil, err
		}
		curReq.Body = body
		curReq.ContentLength = -1
	}
	return curReq, nil
}

// selectService 根据策略选实例。流程:router 过滤 → bannednodes 过滤 → 负载均衡选 + 熔断 Available 检查。
// RR/WRR 复用 pkg/loadbalance,Random 直接 rand 取。
func (t *discoveryTransport) selectService(ctx context.Context, services []discover.ServiceInfo) *httpServiceNode {
	if len(services) == 0 {
		return nil
	}
	// RR/WRR 内部状态基于全量 services 建,选节点时尝试上限用全量长度,
	// 确保能轮到候选集内的节点(router/banned 过滤后子集可能很小)。
	maxAttempts := len(services)
	// 1. 路由过滤
	if t.router != nil {
		services = t.router.Filter(t.serviceName, services)
		if len(services) == 0 {
			return nil
		}
	}
	// 2. bannednodes 过滤
	candidates := filterHTTPBanned(ctx, services)
	if len(candidates) == 0 {
		candidates = services // 全被 ban 则退回全量
	}

	switch t.strategy {
	case HTTPRoundRobin:
		return pickHTTPWithBreaker(ctx, t.breaker, maxAttempts, func() (httpServiceNode, bool) {
			n, ok := t.rr.Next()
			if !ok {
				return httpServiceNode{}, false
			}
			return n, true
		}, candidates)
	case HTTPRandom:
		nodes := toHTTPServiceNodes(candidates)
		if len(nodes) == 0 {
			return nil
		}
		// 从候选里挑 Available 的
		for _, idx := range rand.Perm(len(nodes)) {
			n := nodes[idx]
			if t.breaker.Available(&n.service) {
				return &n
			}
		}
		return nil
	case HTTPWeightedRoundRobin:
		return pickHTTPWithBreaker(ctx, t.breaker, maxAttempts, func() (httpServiceNode, bool) {
			n, ok := t.wrr.Next()
			if !ok {
				return httpServiceNode{}, false
			}
			return n, true
		}, candidates)
	default:
		n := httpServiceNode{service: candidates[0], weight: 100, scheme: "http"}
		return &n
	}
}

// pickHTTPWithBreaker 从负载均衡器选节点,跳过熔断/被ban的节点。最多 maxAttempts 次防死循环。
func pickHTTPWithBreaker(_ context.Context, breaker governancecb.CircuitBreaker, maxAttempts int, next func() (httpServiceNode, bool), candidates []discover.ServiceInfo) *httpServiceNode {
	candidateSet := make(map[string]bool, len(candidates))
	for _, s := range candidates {
		candidateSet[s.Addr] = true
	}
	for range maxAttempts {
		n, ok := next()
		if !ok {
			return nil
		}
		if !candidateSet[n.service.Addr] {
			continue
		}
		if breaker.Available(&n.service) {
			return &n
		}
	}
	return nil
}

// filterHTTPBanned 过滤掉本次请求已禁用的节点。未注入 bannednodes 时原样返回。
func filterHTTPBanned(ctx context.Context, services []discover.ServiceInfo) []discover.ServiceInfo {
	out := services[:0:0]
	for _, s := range services {
		if !bannednodes.IsBanned(ctx, s.Addr) {
			out = append(out, s)
		}
	}
	return out
}

// rebuildBalancers 在服务列表变化时无条件重建 RR/WRR。
func (t *discoveryTransport) rebuildBalancers(services []discover.ServiceInfo) {
	nodes := toHTTPServiceNodes(services)
	t.rr.Update(nodes)
	t.wrr.Update(nodes)
}

// snapshot 读取当前服务列表快照。
func (t *discoveryTransport) snapshot() []discover.ServiceInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.services
}

// refreshServices 从服务发现拉取并缓存服务列表。
func (t *discoveryTransport) refreshServices(ctx context.Context) error {
	services, err := t.discovery.Find(ctx, t.serviceName)
	if err != nil {
		return err
	}
	filtered := t.filterServices(services)
	t.mu.Lock()
	t.services = filtered
	t.rebuildBalancers(filtered)
	t.mu.Unlock()
	return nil
}

func (t *discoveryTransport) filterServices(services []discover.ServiceInfo) []discover.ServiceInfo {
	if t.filter == nil {
		return services
	}
	filtered := make([]discover.ServiceInfo, 0, len(services))
	for _, s := range services {
		if t.filter.Matches(s.Metadata) {
			filtered = append(filtered, s)
		}
	}
	if len(filtered) == 0 {
		slog.Warn("no services match label selector; returning empty (fail-closed)",
			"service", t.serviceName, "total", len(services))
	}
	return filtered
}

// WatchServices 监听服务变化,自动更新缓存。回调在独立 goroutine 执行。
func (t *discoveryTransport) WatchServices(ctx context.Context) error {
	notifyCh := make(chan []discover.ServiceInfo, 1)
	t.bgWg.Add(1)
	go func() {
		defer t.bgWg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case services, ok := <-notifyCh:
				if !ok {
					return
				}
				t.applyServiceUpdate(services)
			}
		}
	}()
	return t.discovery.Watch(ctx, t.serviceName, func(services []discover.ServiceInfo) {
		select {
		case notifyCh <- services:
		default:
			select {
			case <-notifyCh:
			default:
			}
			notifyCh <- services
		}
	})
}

func (t *discoveryTransport) applyServiceUpdate(services []discover.ServiceInfo) {
	filtered := t.filterServices(services)
	t.mu.Lock()
	t.services = filtered
	t.rebuildBalancers(filtered)
	t.mu.Unlock()
	slog.Info("service instances updated",
		"service", t.serviceName, "instances", len(filtered))
}

// Start 启动 watch goroutine。幂等。
func (t *discoveryTransport) Start(ctx context.Context) (err error) {
	t.startOnce.Do(func() {
		err = t.start(ctx)
	})
	return
}

func (t *discoveryTransport) start(ctx context.Context) error {
	if err := t.refreshServices(ctx); err != nil {
		return fmt.Errorf("failed to refresh services: %w", err)
	}
	bgCtx, cancel := context.WithCancel(ctx)
	t.stopFn = cancel
	t.bgWg.Add(1)
	go func() {
		defer t.bgWg.Done()
		t.WatchServices(bgCtx) //nolint:errcheck
	}()
	return nil
}

// autoStart 在首次请求时若 Start 未调用,自动 refresh 一次并打印警告。
func (t *discoveryTransport) autoStart() {
	t.startOnce.Do(func() {
		slog.Warn("discoveryTransport.Start() was not called; "+
			"service list will not update dynamically. Call Start(ctx) explicitly.",
			"service", t.serviceName)
		_ = t.refreshServices(context.Background())
	})
}

// Stop 停止后台 goroutine 并等待退出。幂等。
func (t *discoveryTransport) Stop() {
	t.stopOnce.Do(func() {
		if t.stopFn != nil {
			t.stopFn()
		}
		t.bgWg.Wait()
	})
}

// GetServiceInfo 获取当前缓存的服务列表。
func (t *discoveryTransport) GetServiceInfo() []discover.ServiceInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.services
}

func newDefaultTransport() http.RoundTripper {
	return otelhttp.NewTransport(http.DefaultTransport)
}

var _ http.RoundTripper = (*discoveryTransport)(nil)

func shouldRetryStatus(code int) bool { return code >= 500 }

func isHTTPNonRetryable(err error, resp *http.Response) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return true
		}
	}
	if resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return true
	}
	return false
}
