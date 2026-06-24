package grpcclient

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DialOption 拨号选项
type DialOption func(*dialConfig)

// dialConfig 拨号配置
type dialConfig struct {
	// 直连模式
	direct bool

	registry    discover.Discovery
	labelFilter *ServiceLabelFilter
	grpcOpts    []grpc.DialOption
	timeout     time.Duration
	loadBalancer string

	// 地域过滤
	regions      []string
	zones        []string
	campuses     []string
	environments []string

	// 版本过滤
	versions []string
}

// WithDirect 直连模式，target 直接作为 addr 传给 gRPC，不走服务发现。
// 用法：grpcclient.DialContext(ctx, "127.0.0.1:8080", grpcclient.WithDirect())
func WithDirect() DialOption {
	return func(c *dialConfig) {
		c.direct = true
	}
}

// WithInsecure 明文连接（不加密），通常用于开发或内网可信环境。
func WithInsecure() DialOption {
	return WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// WithRegistry 设置服务注册中心
func WithRegistry(registry discover.Discovery) DialOption {
	return func(c *dialConfig) {
		c.registry = registry
	}
}

// WithLabelFilter 设置标签过滤器
func WithLabelFilter(filter *ServiceLabelFilter) DialOption {
	return func(c *dialConfig) {
		c.labelFilter = filter
	}
}

// WithGRPCDialOptions 设置底层 gRPC 连接选项
func WithGRPCDialOptions(opts ...grpc.DialOption) DialOption {
	return func(c *dialConfig) {
		c.grpcOpts = append(c.grpcOpts, opts...)
	}
}

// WithTimeout 设置连接超时时间
func WithTimeout(timeout time.Duration) DialOption {
	return func(c *dialConfig) {
		c.timeout = timeout
	}
}

// WithLoadBalancer 设置负载均衡策略（服务发现模式有效）
func WithLoadBalancer(strategy string) DialOption {
	return func(c *dialConfig) {
		c.loadBalancer = strategy
	}
}

// WithRegionFilter 设置地域过滤
func WithRegionFilter(regions, zones, campuses, environments []string) DialOption {
	return func(c *dialConfig) {
		c.regions = regions
		c.zones = zones
		c.campuses = campuses
		c.environments = environments
	}
}

// WithEnvironment 设置环境过滤
func WithEnvironment(env string) DialOption {
	return func(c *dialConfig) {
		c.environments = []string{env}
	}
}

// WithVersion 只路由到指定版本的实例，用于灰度发布或版本级隔离。
// 服务端需通过 grpcserver.WithVersion("v2") 将版本写入注册信息。
//
//	conn, _ := grpcclient.Dial("etcd://host/svc", grpcclient.WithVersion("v2"))
func WithVersion(version string) DialOption {
	return func(c *dialConfig) {
		c.versions = []string{version}
	}
}

// WithVersionIn 路由到版本在给定集合中的实例，支持同时灰度多个版本。
//
//	conn, _ := grpcclient.Dial("etcd://host/svc", grpcclient.WithVersionIn("v2", "v3"))
func WithVersionIn(versions ...string) DialOption {
	return func(c *dialConfig) {
		c.versions = versions
	}
}

// WithRegion 设置地域过滤
func WithRegion(region string) DialOption {
	return func(c *dialConfig) {
		c.regions = []string{region}
	}
}

// DialContext 统一拨号入口，支持两种模式：
//
//	直连：grpcclient.DialContext(ctx, "127.0.0.1:8080", grpcclient.WithDirect())
//	服务发现：grpcclient.DialContext(ctx, "etcd://127.0.0.1:2379/my-service")
//	          grpcclient.DialContext(ctx, "nacos://127.0.0.1:8848/my-service?env=prod")
func DialContext(ctx context.Context, target string, opts ...DialOption) (*grpc.ClientConn, error) {
	cfg := &dialConfig{
		timeout:      5 * time.Second,
		loadBalancer: "round_robin",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// 直连模式：target 就是 addr
	if cfg.direct {
		return dialDirect(ctx, target, cfg)
	}

	// xDS 模式：target 形如 xds:///service，由 xDS 控制平面下发端点与负载均衡策略。
	// 需先空导入 github.com/rushteam/beauty/pkg/client/grpcclient/xds 以注册 xds:// resolver，
	// 并通过 GRPC_XDS_BOOTSTRAP / GRPC_XDS_BOOTSTRAP_CONFIG 提供引导配置。
	if isXDSTarget(target) {
		return dialXDS(target, cfg)
	}

	// 服务发现模式
	return dialWithDiscovery(ctx, target, cfg)
}

// isXDSTarget 判断 target 是否为 xDS 方案（xds:///service）。
func isXDSTarget(target string) bool {
	return strings.HasPrefix(target, "xds:")
}

// dialXDS xDS 模式：target 原样交给 gRPC 内置的 xds resolver 处理。
// 端点发现与负载均衡均由控制平面下发，故本地不设 loadBalancingPolicy，
// 也不应用 WithRegistry / 标签过滤（这些仅服务发现模式有效）。
// 传输凭证需由调用方提供：明文用 grpcclient.WithInsecure()，
// mTLS 用 grpcclient/xds.WithCredentials()。
func dialXDS(target string, cfg *dialConfig) (*grpc.ClientConn, error) {
	opts := standardDialOpts()
	opts = append(opts, cfg.grpcOpts...)
	return grpc.NewClient(target, opts...)
}

// Dial 不需要 context 的简化版本
func Dial(target string, opts ...DialOption) (*grpc.ClientConn, error) {
	return DialContext(context.Background(), target, opts...)
}

// dialDirect 直连模式
func dialDirect(_ context.Context, addr string, cfg *dialConfig) (*grpc.ClientConn, error) {
	var dOpts []directOption
	dOpts = append(dOpts, withDirectAddr(addr))
	if len(cfg.grpcOpts) > 0 {
		dOpts = append(dOpts, withDirectDialOpts(cfg.grpcOpts...))
	}
	if cfg.loadBalancer != "" {
		dOpts = append(dOpts, withDirectBalancingPolicy(cfg.loadBalancer))
	}

	c, err := newDirectClient(dOpts...)
	if err != nil {
		return nil, err
	}
	return c.ClientConn, nil
}

// dialWithDiscovery 服务发现模式
func dialWithDiscovery(ctx context.Context, target string, cfg *dialConfig) (*grpc.ClientConn, error) {
	serviceName, registry, params, err := parseTarget(target)
	if err != nil {
		return nil, fmt.Errorf("invalid target %s: %w", target, err)
	}

	// WithRegistry 选项优先级高于 URL 解析
	if cfg.registry != nil {
		registry = cfg.registry
	}

	if registry == nil {
		return nil, fmt.Errorf("no registry for target %s; use WithRegistry() or provide a registry URL (etcd://, nacos://, ...)", target)
	}

	// 构建标签过滤器
	if cfg.labelFilter == nil && len(params) > 0 {
		cfg.labelFilter = buildFilterFromParams(params)
	}
	if cfg.labelFilter == nil && (len(cfg.regions) > 0 || len(cfg.zones) > 0 ||
		len(cfg.campuses) > 0 || len(cfg.environments) > 0 || len(cfg.versions) > 0) {
		cfg.labelFilter = NewLabelFilter().
			WithRegionIn(cfg.regions...).
			WithZoneIn(cfg.zones...).
			WithCampusIn(cfg.campuses...).
			WithEnvironmentIn(cfg.environments...).
			WithVersionIn(cfg.versions...)
	} else if cfg.labelFilter != nil && len(cfg.versions) > 0 {
		// labelFilter 已存在（由 WithLabelFilter 设置），追加 version 条件
		cfg.labelFilter.WithVersionIn(cfg.versions...)
	}

	var clientOpts []ServiceDiscoveryOption
	if cfg.labelFilter != nil {
		clientOpts = append(clientOpts, WithDiscoveryLabelFilter(cfg.labelFilter))
	}
	if len(cfg.grpcOpts) > 0 {
		clientOpts = append(clientOpts, WithDiscoveryDialOptions(cfg.grpcOpts...))
	}
	if cfg.loadBalancer != "" {
		clientOpts = append(clientOpts, WithDiscoveryStrategy(loadBalancerToStrategy(cfg.loadBalancer)))
	}

	client := NewServiceDiscoveryClient(registry, serviceName, clientOpts...)

	dialCtx := ctx
	if cfg.timeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
	}

	return client.GetClient(dialCtx)
}

// loadBalancerToStrategy 将字符串策略名映射为 LoadBalanceStrategy
func loadBalancerToStrategy(lb string) LoadBalanceStrategy {
	switch lb {
	case "random":
		return Random
	case "weighted_round_robin":
		return WeightedRoundRobin
	case "least_connections":
		return LeastConnections
	default:
		return RoundRobin
	}
}

// parseTarget 解析目标地址，返回服务名、注册中心、URL 查询参数
func parseTarget(target string) (serviceName string, registry discover.Discovery, params map[string]string, err error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", nil, nil, err
	}

	serviceName = strings.TrimPrefix(u.Path, "/")
	if serviceName == "" {
		serviceName = u.Host
	}

	params = make(map[string]string)
	for k, v := range u.Query() {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}

	registry, err = createRegistryFromScheme(u.Scheme, u)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to create registry for scheme %s: %w", u.Scheme, err)
	}

	return serviceName, registry, params, nil
}

// buildFilterFromParams 从 URL 查询参数构建标签过滤器
func buildFilterFromParams(params map[string]string) *ServiceLabelFilter {
	filter := NewLabelFilter()
	hasFilter := false

	if env := params["env"]; env != "" {
		filter = filter.WithEnvironmentIn(env)
		hasFilter = true
	}
	if env := params["environment"]; env != "" {
		filter = filter.WithEnvironmentIn(env)
		hasFilter = true
	}
	if region := params["region"]; region != "" {
		filter = filter.WithRegionIn(strings.Split(region, ",")...)
		hasFilter = true
	}
	if zone := params["zone"]; zone != "" {
		filter = filter.WithZoneIn(strings.Split(zone, ",")...)
		hasFilter = true
	}
	if campus := params["campus"]; campus != "" {
		filter = filter.WithCampusIn(strings.Split(campus, ",")...)
		hasFilter = true
	}
	if version := params["version"]; version != "" {
		filter = filter.WithVersionIn(strings.Split(version, ",")...)
		hasFilter = true
	}
	for k, v := range params {
		if !isReservedParam(k) {
			filter = filter.WithMatchLabel(k, v)
			hasFilter = true
		}
	}

	if !hasFilter {
		return nil
	}
	return filter
}

func isReservedParam(param string) bool {
	return slices.Contains([]string{"env", "environment", "region", "zone", "campus", "namespace", "group", "version"}, param)
}

func getDefaultRegistry() discover.Discovery {
	return nil
}

// createRegistryFromScheme 使用插件机制创建注册中心
func createRegistryFromScheme(scheme string, targetURL *url.URL) (discover.Discovery, error) {
	switch scheme {
	case "beauty":
		return getDefaultRegistry(), nil
	default:
		manager := discover.GetManager()
		return manager.CreateRegistryFromURL(targetURL)
	}
}
