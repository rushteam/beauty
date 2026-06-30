package resty

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rushteam/beauty/pkg/loadbalance"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

// HTTPBalanceStrategy HTTP 负载均衡策略。不含 LeastConnections——
// HTTP 客户端不维护连接池状态,无法查 in-flight/连接状态。
type HTTPBalanceStrategy int

const (
	HTTPRoundRobin HTTPBalanceStrategy = iota
	HTTPRandom
	HTTPWeightedRoundRobin
)

// httpServiceNode 适配 discover.ServiceInfo 到 loadbalance.Node。
// Scheme 从 Metadata["scheme"] 读(默认 "http");Weight 从 Metadata["weight"] 解析(默认 100)。
type httpServiceNode struct {
	service discover.ServiceInfo
	weight  int
	scheme  string
}

func (n httpServiceNode) ID() string     { return n.service.Addr }
func (n httpServiceNode) Weight() int    { return n.weight }
func (n httpServiceNode) Scheme() string { return n.scheme }

// toHTTPServiceNodes 把 []discover.ServiceInfo 转成 []httpServiceNode。
func toHTTPServiceNodes(services []discover.ServiceInfo) []httpServiceNode {
	nodes := make([]httpServiceNode, 0, len(services))
	for _, s := range services {
		w := 100
		if v, ok := s.Metadata["weight"]; ok {
			if p, err := strconv.Atoi(v); err == nil && p > 0 {
				w = p
			}
		}
		scheme := "http"
		if v, ok := s.Metadata["scheme"]; ok && v != "" {
			scheme = v
		}
		nodes = append(nodes, httpServiceNode{service: s, weight: w, scheme: scheme})
	}
	return nodes
}

// HTTPDiscoveryOption 服务发现配置选项。同时作用于 transport 与 client 包装层
// (二者共享 discoveryConfig),故 Option 接收 *discoveryConfig。
// WithHTTPTimeout 是例外——它配置 http.Client 超时,只在 client 层生效。
type HTTPDiscoveryOption func(*discoveryConfig)

// WithHTTPStrategy 设置负载均衡策略(默认 HTTPRoundRobin)。
func WithHTTPStrategy(s HTTPBalanceStrategy) HTTPDiscoveryOption {
	return func(c *discoveryConfig) { c.strategy = s }
}

// WithHTTPLabelFilter 设置标签过滤器(地域/版本/zone 等),直接复用 selector.LabelFilter。
func WithHTTPLabelFilter(f *selector.LabelFilter) HTTPDiscoveryOption {
	return func(c *discoveryConfig) { c.filter = f }
}

// WithHTTPMaxRetries 设置额外重试次数(0=不重试,总尝试=maxRetries+1)。
func WithHTTPMaxRetries(n int) HTTPDiscoveryOption {
	return func(c *discoveryConfig) { c.maxRetries = n }
}

// WithHTTPRetryDelay 设置指数退避 base(默认 1s,实际等待 base*2^i ± 25% jitter)。
func WithHTTPRetryDelay(d time.Duration) HTTPDiscoveryOption {
	return func(c *discoveryConfig) { c.retryDelay = d }
}

// WithHTTPRetryOnDifferentNode 设置重试时是否换节点(默认 true)。
// true:每次重试重新选实例,处理节点彻底不可用(对齐 gRPC failover);
// false:重试复用同一 URL,仅对网络抖动有效。
func WithHTTPRetryOnDifferentNode(on bool) HTTPDiscoveryOption {
	return func(c *discoveryConfig) { c.retryOnDiffNode = on }
}

// ServiceDiscoveryHTTPClient 基于服务发现的 HTTP 客户端。是 discoveryTransport(RoundTripper)
// 的薄包装:持有 *http.Client(Transport=discoveryTransport),提供便捷的 Do/DoWith/NewRequest。
//
// 调用方也可只用 NewDiscoveryTransport 拿到 RoundTripper,塞进自己的 http.Client,
// 跳过本包装层——适用于已有 http.Client 管理逻辑的场景。
//
// 生命周期:New → Start(ctx)(启动 watch)→ Do/NewRequest → Stop/Close。
// 未调用 Start 时首次 Do 会 autoStart(仅 refresh 一次,不启动 watch,并打印警告)。
type ServiceDiscoveryHTTPClient struct {
	httpClient *http.Client
	transport  *discoveryTransport
}

// NewServiceDiscoveryHTTPClient 创建基于服务发现的 HTTP 客户端。
// 内部构造 discoveryTransport 作为 RoundTripper,包成 *http.Client。
func NewServiceDiscoveryHTTPClient(discovery discover.Discovery, serviceName string, opts ...HTTPDiscoveryOption) *ServiceDiscoveryHTTPClient {
	cfg := discoveryConfig{
		discovery:       discovery,
		serviceName:     serviceName,
		maxRetries:      1,
		retryDelay:      time.Second,
		retryOnDiffNode: true,
		timeout:         30 * time.Second,
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
	return &ServiceDiscoveryHTTPClient{
		httpClient: &http.Client{Transport: t, Timeout: cfg.timeout},
		transport:  t,
	}
}

// WithHTTPTimeout 覆盖底层 http.Client 超时(默认 30s)。传 0 表示不设超时。
// 仅在 ServiceDiscoveryHTTPClient 上生效(配置 *http.Client.Timeout)。
func WithHTTPTimeout(d time.Duration) HTTPDiscoveryOption {
	return func(c *discoveryConfig) { c.timeout = d }
}

// Start 启动客户端:拉取初始服务列表并启动 watch。幂等。
// 调用 Stop 或取消传入 ctx 均可停止后台 goroutine。
func (c *ServiceDiscoveryHTTPClient) Start(ctx context.Context) error {
	return c.transport.Start(ctx)
}

// Stop 停止后台 goroutine 并等待退出。幂等。
func (c *ServiceDiscoveryHTTPClient) Stop() {
	c.transport.Stop()
}

// Close 停止后台 goroutine。HTTP 客户端无长连接池需关闭,等价于 Stop。
func (c *ServiceDiscoveryHTTPClient) Close() error {
	c.Stop()
	return nil
}

// NewRequest 选实例并拼好 URL,返回 *http.Request。调用方自行设置 body/header 后用 Do 发送。
// method 为 HTTP 方法,path 为相对路径(如 "/api/users")。
func (c *ServiceDiscoveryHTTPClient) NewRequest(ctx context.Context, method, path string) (*http.Request, error) {
	// URL.Host 会被 transport 改写,这里先放占位 host(调用方传的 path 作为 URL.Path)
	r, err := http.NewRequestWithContext(ctx, method, path, nil)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Do 发送已构造好的 *http.Request(通常由 NewRequest 创建,也可外部构造)。
// transport 会改写 URL.Host 为选中的实例地址,并按配置重试。
// 调用方负责关闭返回的 resp.Body。
func (c *ServiceDiscoveryHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
}

// DoWith 便捷形式:选实例 + 拼 URL + 发送,一步到位。
// method 为 HTTP 方法,path 为相对路径(如 "/api/users"),body 为可选 io.Reader。
func (c *ServiceDiscoveryHTTPClient) DoWith(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	return c.httpClient.Do(r)
}

// GetServiceInfo 获取当前缓存的服务列表。
func (c *ServiceDiscoveryHTTPClient) GetServiceInfo() []discover.ServiceInfo {
	return c.transport.GetServiceInfo()
}
