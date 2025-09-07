package grpcclient

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// DialOption 拨号选项
type DialOption func(*dialConfig)

// dialConfig 拨号配置
type dialConfig struct {
	registry      discover.Discovery
	labelFilter   *ServiceLabelFilter
	grpcOpts      []grpc.DialOption
	timeout       time.Duration
	namespace     string
	loadBalancer  string
	disableRouter bool
	// 地域信息（向后兼容）
	regions      []string
	zones        []string
	campuses     []string
	environments []string
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

// WithGRPCDialOptions 设置 gRPC 连接选项
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

// WithNamespace 设置命名空间
func WithNamespace(namespace string) DialOption {
	return func(c *dialConfig) {
		c.namespace = namespace
	}
}

// WithLoadBalancer 设置负载均衡策略
func WithLoadBalancer(strategy string) DialOption {
	return func(c *dialConfig) {
		c.loadBalancer = strategy
	}
}

// WithDisableRouter 禁用路由
func WithDisableRouter() DialOption {
	return func(c *dialConfig) {
		c.disableRouter = true
	}
}

// WithRegionFilter 设置地域过滤（向后兼容）
func WithRegionFilter(regions, zones, campuses, environments []string) DialOption {
	return func(c *dialConfig) {
		c.regions = regions
		c.zones = zones
		c.campuses = campuses
		c.environments = environments
	}
}

// WithEnvironment 设置环境过滤（便捷方法）
func WithEnvironment(env string) DialOption {
	return func(c *dialConfig) {
		c.environments = []string{env}
	}
}

// WithRegion 设置地域过滤（便捷方法）
func WithRegion(region string) DialOption {
	return func(c *dialConfig) {
		c.regions = []string{region}
	}
}

// DialContext 类似 Polaris 风格的简化拨号 API
// 支持的 target 格式:
//   - beauty://serviceName                          - 使用默认注册中心
//   - beauty://serviceName?env=production           - 带环境参数
//   - etcd://endpoints/serviceName                  - 使用 etcd
//   - nacos://endpoints/serviceName                 - 使用 nacos
func DialContext(ctx context.Context, target string, opts ...DialOption) (*grpc.ClientConn, error) {
	// 解析 target
	serviceName, registry, params, err := parseTarget(target)
	if err != nil {
		return nil, fmt.Errorf("invalid target %s: %w", target, err)
	}

	// 构建配置
	config := &dialConfig{
		registry:     registry,
		grpcOpts:     []grpc.DialOption{},
		timeout:      time.Second * 5,
		loadBalancer: "round_robin",
	}

	// 应用选项
	for _, opt := range opts {
		opt(config)
	}

	// 如果没有设置注册中心且使用的是默认 beauty 协议，返回错误
	if config.registry == nil && registry == nil {
		return nil, fmt.Errorf("no registry provided, use WithRegistry() option or provide explicit registry URL")
	}

	// 优先使用配置中的注册中心
	if config.registry != nil {
		registry = config.registry
	}

	// 从 URL 参数构建过滤器
	if config.labelFilter == nil && len(params) > 0 {
		config.labelFilter = buildFilterFromParams(params)
	}

	// 从地域信息构建过滤器（向后兼容）
	if config.labelFilter == nil && (len(config.regions) > 0 || len(config.zones) > 0 ||
		len(config.campuses) > 0 || len(config.environments) > 0) {
		config.labelFilter = NewLabelFilter().
			WithRegionIn(config.regions...).
			WithZoneIn(config.zones...).
			WithCampusIn(config.campuses...).
			WithEnvironmentIn(config.environments...)
	}

	// 设置默认 gRPC 选项
	if len(config.grpcOpts) == 0 {
		config.grpcOpts = []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                time.Second * 20,
				Timeout:             time.Second * 10,
				PermitWithoutStream: true,
			}),
			grpc.WithIdleTimeout(time.Second * 10),
		}
	}

	// 添加负载均衡配置
	if config.loadBalancer != "" {
		serviceConfig := fmt.Sprintf(`{"loadBalancingPolicy":"%s"}`, config.loadBalancer)
		config.grpcOpts = append(config.grpcOpts, grpc.WithDefaultServiceConfig(serviceConfig))
	}

	// 创建服务发现客户端
	var clientOpts []ServiceDiscoveryOption
	if config.labelFilter != nil {
		clientOpts = append(clientOpts, WithDiscoveryLabelFilter(config.labelFilter))
	}
	clientOpts = append(clientOpts, WithDiscoveryDialOptions(config.grpcOpts...))

	client := NewServiceDiscoveryClient(config.registry, serviceName, clientOpts...)

	// 创建连接（带超时）
	dialCtx := ctx
	if config.timeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, config.timeout)
		defer cancel()
	}

	return client.GetClient(dialCtx)
}

// parseTarget 解析目标地址
func parseTarget(target string) (serviceName string, registry discover.Discovery, params map[string]string, err error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", nil, nil, err
	}

	serviceName = strings.TrimPrefix(u.Path, "/")
	if serviceName == "" {
		serviceName = u.Host
	}

	// 解析查询参数
	params = make(map[string]string)
	for k, v := range u.Query() {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}

	// 根据 scheme 创建注册中心
	switch u.Scheme {
	case "beauty":
		// 使用默认注册中心或从环境变量获取
		registry = getDefaultRegistry()
	case "etcd", "nacos":
		// 对于 etcd 和 nacos，需要用户提供注册中心实例
		return "", nil, nil, fmt.Errorf("scheme %s requires explicit registry via WithRegistry option", u.Scheme)
	default:
		return "", nil, nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}

	return serviceName, registry, params, nil
}

// buildFilterFromParams 从 URL 参数构建过滤器
func buildFilterFromParams(params map[string]string) *ServiceLabelFilter {
	filter := NewLabelFilter()
	hasFilter := false

	// 处理环境参数
	if env := params["env"]; env != "" {
		filter = filter.WithEnvironmentIn(env)
		hasFilter = true
	}
	if env := params["environment"]; env != "" {
		filter = filter.WithEnvironmentIn(env)
		hasFilter = true
	}

	// 处理地域参数
	if region := params["region"]; region != "" {
		regions := strings.Split(region, ",")
		filter = filter.WithRegionIn(regions...)
		hasFilter = true
	}

	// 处理可用区参数
	if zone := params["zone"]; zone != "" {
		zones := strings.Split(zone, ",")
		filter = filter.WithZoneIn(zones...)
		hasFilter = true
	}

	// 处理园区参数
	if campus := params["campus"]; campus != "" {
		campuses := strings.Split(campus, ",")
		filter = filter.WithCampusIn(campuses...)
		hasFilter = true
	}

	// 处理其他标签
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

// isReservedParam 检查是否为保留参数
func isReservedParam(param string) bool {
	reserved := []string{"env", "environment", "region", "zone", "campus", "namespace", "group"}
	for _, r := range reserved {
		if param == r {
			return true
		}
	}
	return false
}

// getDefaultRegistry 获取默认注册中心
func getDefaultRegistry() discover.Discovery {
	// 这里可以从环境变量或配置文件获取默认注册中心
	// 暂时返回 nil，需要用户显式提供注册中心
	// 在实际使用中，可以通过全局配置或环境变量来设置默认注册中心
	return nil
}

// Dial 简化版本，不需要 context
func Dial(target string, opts ...DialOption) (*grpc.ClientConn, error) {
	return DialContext(context.Background(), target, opts...)
}
