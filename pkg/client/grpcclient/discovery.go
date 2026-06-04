package grpcclient

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/rushteam/beauty/pkg/service/discover"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// WithDiscoveryInsecure 明确声明使用明文连接（不加密）。
// 生产环境应通过 WithDiscoveryDialOptions(grpc.WithTransportCredentials(...)) 提供 TLS 凭证；
// 此选项仅用于开发或内网可信环境。
func WithDiscoveryInsecure() ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.dialOpts = append(c.dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// LoadBalanceStrategy 负载均衡策略
type LoadBalanceStrategy int

const (
	RoundRobin         LoadBalanceStrategy = iota
	Random
	WeightedRoundRobin
	LeastConnections
)

// ServiceDiscoveryClient 基于服务发现的gRPC客户端
type ServiceDiscoveryClient struct {
	discovery   discover.Discovery
	serviceName string

	mu       sync.RWMutex
	clients  map[string]*grpc.ClientConn
	services []discover.ServiceInfo

	retryPolicy *RetryPolicy // nil 表示使用 DefaultRetryPolicy

	strategyVal LoadBalanceStrategy
	rrIndex     atomic.Int64 // RoundRobin 游标，原子操作避免锁内写
	wrrIndex    int          // WeightedRoundRobin 游标，受 mu.Lock 保护
	wrrRemain   int          // 当前节点剩余配额
	wrrServices []wrrEntry   // 权重表，服务列表变化时重建

	dialOpts           []grpc.DialOption
	unaryInterceptors  []grpc.UnaryClientInterceptor
	streamInterceptors []grpc.StreamClientInterceptor
	labelFilter        *ServiceLabelFilter

	// 健康检查
	healthCheck   bool
	checkInterval time.Duration

	// 故障重试
	maxRetries int
	retryDelay time.Duration

	// 连接排空
	drainTimeout time.Duration

	// 后台 goroutine 生命周期
	startOnce sync.Once
	stopOnce  sync.Once
	stopFn    context.CancelFunc
	bgWg      sync.WaitGroup
}

type wrrEntry struct {
	service discover.ServiceInfo
	weight  int
}

// ServiceDiscoveryOption 服务发现客户端选项
type ServiceDiscoveryOption func(*ServiceDiscoveryClient)

// NewServiceDiscoveryClient 创建基于服务发现的客户端
func NewServiceDiscoveryClient(discovery discover.Discovery, serviceName string, opts ...ServiceDiscoveryOption) *ServiceDiscoveryClient {
	c := &ServiceDiscoveryClient{
		discovery:   discovery,
		serviceName: serviceName,
		clients:     make(map[string]*grpc.ClientConn),
		dialOpts: []grpc.DialOption{
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                time.Second * 20,
				Timeout:             time.Second * 10,
				PermitWithoutStream: true,
			}),
			grpc.WithIdleTimeout(time.Second * 10),
		},
		healthCheck:   true,
		checkInterval: time.Second * 30,
		maxRetries:    1,
		retryDelay:    time.Second,
		drainTimeout:  5 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithDiscoveryRegionFilter 追加地域过滤条件（可与 WithDiscoveryLabelFilter 叠加）
func WithDiscoveryRegionFilter(regions, zones, campuses, environments []string) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		if c.labelFilter == nil {
			c.labelFilter = NewLabelFilter()
		}
		c.labelFilter.
			WithRegionIn(regions...).
			WithZoneIn(zones...).
			WithCampusIn(campuses...).
			WithEnvironmentIn(environments...)
	}
}

// WithDiscoveryVersionFilter 只路由到 version 在给定集合中的实例。
// 服务端通过 grpcserver.WithVersion("v2") 注册版本信息，客户端用此 Option 过滤。
//
// 灰度示例：同时保留 v1（稳定）和 v2（灰度），按流量比例路由见 WithDiscoveryStrategy。
//
//	// 只调 v2 实例
//	client := grpcclient.NewServiceDiscoveryClient(reg, "order-svc",
//	    grpcclient.WithDiscoveryVersionFilter("v2"),
//	)
func WithDiscoveryVersionFilter(versions ...string) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		if c.labelFilter == nil {
			c.labelFilter = NewLabelFilter()
		}
		c.labelFilter.WithVersionIn(versions...)
	}
}

// WithDiscoveryLabelFilter 设置标签过滤器，替换由 WithDiscoveryRegionFilter 设置的过滤条件。
// 若需同时使用两种过滤，请在同一个 ServiceLabelFilter 上链式调用后再传入。
func WithDiscoveryLabelFilter(filter *ServiceLabelFilter) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.labelFilter = filter
	}
}

// WithDiscoveryDialOptions 设置连接选项
func WithDiscoveryDialOptions(opts ...grpc.DialOption) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.dialOpts = append(c.dialOpts, opts...)
	}
}

// WithUnaryInterceptors 设置一元拦截器
func WithUnaryInterceptors(interceptors ...grpc.UnaryClientInterceptor) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.unaryInterceptors = append(c.unaryInterceptors, interceptors...)
	}
}

// WithStreamInterceptors 设置流拦截器
func WithStreamInterceptors(interceptors ...grpc.StreamClientInterceptor) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.streamInterceptors = append(c.streamInterceptors, interceptors...)
	}
}

// WithDiscoveryStrategy 设置负载均衡策略
func WithDiscoveryStrategy(strategy LoadBalanceStrategy) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.strategyVal = strategy
	}
}

// WithDiscoveryHealthCheck 设置健康检查
func WithDiscoveryHealthCheck(enabled bool, interval time.Duration) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.healthCheck = enabled
		c.checkInterval = interval
	}
}

// WithDiscoveryFailover 设置故障重试
func WithDiscoveryFailover(maxRetries int, retryDelay time.Duration) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.maxRetries = maxRetries
		c.retryDelay = retryDelay
	}
}

// WithDiscoveryDrainTimeout 设置服务实例从发现列表移除后，连接的排空等待时间。
// 在此期间连接不会被关闭，正在进行的请求有机会完成。默认 5s，设为 0 立即关闭。
func WithDiscoveryDrainTimeout(d time.Duration) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.drainTimeout = d
	}
}

// WithDiscoveryRetryPolicy 设置 gRPC 原生 retry policy，覆盖默认策略。
// 传入零值 RetryPolicy{} 或空 RetryableStatusCodes 可禁用重试。
//
// 示例——对 UNAVAILABLE 和 RESOURCE_EXHAUSTED 均重试，最多 5 次：
//
//	WithDiscoveryRetryPolicy(grpcclient.DefaultRetryPolicy().WithResourceExhausted())
func WithDiscoveryRetryPolicy(p RetryPolicy) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.retryPolicy = &p
	}
}

// Start 启动客户端：拉取初始服务列表，启动 watch 和健康检查。
// 调用 Stop() 或取消传入的 ctx 均可停止后台 goroutine。
// Start 是幂等的，多次调用只有第一次生效。
func (c *ServiceDiscoveryClient) Start(ctx context.Context) (err error) {
	c.startOnce.Do(func() {
		err = c.start(ctx)
	})
	return
}

func (c *ServiceDiscoveryClient) start(ctx context.Context) error {
	if err := c.refreshServices(ctx); err != nil {
		return fmt.Errorf("failed to refresh services: %w", err)
	}
	bgCtx, cancel := context.WithCancel(ctx)
	c.stopFn = cancel

	c.bgWg.Add(1)
	go func() {
		defer c.bgWg.Done()
		c.WatchServices(bgCtx) //nolint:errcheck
	}()

	if c.healthCheck {
		c.bgWg.Add(1)
		go func() {
			defer c.bgWg.Done()
			c.healthCheckLoop(bgCtx)
		}()
	}
	return nil
}

// autoStart 在 GetClient 首次调用时，若 Start 从未调用，用 background context 自动启动。
// 仅做一次 refresh，不启动 watch/healthCheck，并打印警告提示用户显式调用 Start。
func (c *ServiceDiscoveryClient) autoStart() {
	c.startOnce.Do(func() {
		slog.Warn("ServiceDiscoveryClient.Start() was not called; "+
			"service list will not update dynamically. Call Start(ctx) explicitly.",
			"service", c.serviceName)
		_ = c.refreshServices(context.Background())
	})
}

// Stop 停止后台 goroutine 并等待它们退出
func (c *ServiceDiscoveryClient) Stop() {
	c.stopOnce.Do(func() {
		if c.stopFn != nil {
			c.stopFn()
		}
		c.bgWg.Wait()
	})
}

// GetClient 获取一个连接，按负载均衡策略选择实例
func (c *ServiceDiscoveryClient) GetClient(ctx context.Context) (*grpc.ClientConn, error) {
	c.autoStart()

	c.mu.RLock()
	services := c.services
	c.mu.RUnlock()

	// 服务列表为空时尝试实时拉取
	if len(services) == 0 {
		if err := c.refreshServices(ctx); err != nil {
			return nil, fmt.Errorf("no instances found for service %s", c.serviceName)
		}
		c.mu.RLock()
		services = c.services
		c.mu.RUnlock()
	}

	if len(services) == 0 {
		return nil, fmt.Errorf("no instances found for service %s", c.serviceName)
	}

	service := c.selectService(services)
	if service == nil {
		return nil, fmt.Errorf("no suitable instance for service %s", c.serviceName)
	}
	return c.getOrCreateConn(service)
}

// Call 调用服务方法，支持指数退避重试（带 ±25% jitter）。
// maxRetries 为额外重试次数，0 表示不重试，总调用次数为 maxRetries+1。
// context.Canceled / context.DeadlineExceeded 不重试，直接返回。
func (c *ServiceDiscoveryClient) Call(ctx context.Context, method string, req, resp any, opts ...grpc.CallOption) error {
	attempts := c.maxRetries + 1
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := c.GetClient(ctx)
		if err != nil {
			lastErr = err
		} else if err = conn.Invoke(ctx, method, req, resp, opts...); err == nil {
			return nil
		} else {
			lastErr = err
		}

		// 不可重试错误：调用方已取消或超时
		if isNonRetryable(lastErr) {
			return lastErr
		}

		if i < attempts-1 {
			// 指数退避：base * 2^i，加 ±25% jitter
			base := c.retryDelay * (1 << i)
			jitter := time.Duration(rand.Int64N(int64(base/2))) - base/4
			delay := base + jitter
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("failed after %d retries: %w", c.maxRetries, lastErr)
}

// isNonRetryable 判断错误是否不应重试
func isNonRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Canceled, codes.InvalidArgument, codes.NotFound,
			codes.AlreadyExists, codes.PermissionDenied, codes.Unauthenticated:
			return true
		}
	}
	return false
}

// selectService 根据策略选择服务实例，调用方持有 mu.RLock 或已复制 slice
func (c *ServiceDiscoveryClient) selectService(services []discover.ServiceInfo) *discover.ServiceInfo {
	if len(services) == 0 {
		return nil
	}
	switch c.strategyVal {
	case RoundRobin:
		// atomic 自增，不需要写锁
		idx := int(c.rrIndex.Add(1)) % len(services)
		return &services[idx]
	case Random:
		return &services[rand.IntN(len(services))]
	case WeightedRoundRobin:
		return c.weightedRoundRobin(services)
	case LeastConnections:
		return c.leastConnections(services)
	default:
		return &services[0]
	}
}

// weightedRoundRobin 平滑加权轮询（Nginx smooth WRR）
func (c *ServiceDiscoveryClient) weightedRoundRobin(services []discover.ServiceInfo) *discover.ServiceInfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 服务列表变化时重建权重表
	if len(c.wrrServices) != len(services) {
		c.wrrServices = make([]wrrEntry, len(services))
		for i, s := range services {
			w := 100
			if v, ok := s.Metadata["weight"]; ok {
				if p, err := strconv.Atoi(v); err == nil && p > 0 {
					w = p
				}
			}
			c.wrrServices[i] = wrrEntry{service: s, weight: w}
		}
		c.wrrIndex = 0
		c.wrrRemain = 0
	}

	// 当前节点还有配额
	if c.wrrRemain > 0 {
		c.wrrRemain--
		return &c.wrrServices[c.wrrIndex].service
	}

	// 移动到下一个节点
	c.wrrIndex = (c.wrrIndex + 1) % len(c.wrrServices)
	c.wrrRemain = c.wrrServices[c.wrrIndex].weight - 1
	return &c.wrrServices[c.wrrIndex].service
}

// leastConnections 优先选择尚未建立连接的节点；若均已连接则选第一个 READY 节点，兜底随机。
// grpc.ClientConn 不暴露 in-flight 请求数，无法做真正的最少连接，这里以"无连接"作为代理指标。
func (c *ServiceDiscoveryClient) leastConnections(services []discover.ServiceInfo) *discover.ServiceInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var ready *discover.ServiceInfo
	for i := range services {
		conn, ok := c.clients[services[i].Addr]
		if !ok {
			return &services[i]
		}
		if ready == nil && conn.GetState() == connectivity.Ready {
			ready = &services[i]
		}
	}
	if ready != nil {
		return ready
	}
	return &services[rand.IntN(len(services))]
}

// getOrCreateConn 获取或建立到指定地址的连接
func (c *ServiceDiscoveryClient) getOrCreateConn(service *discover.ServiceInfo) (*grpc.ClientConn, error) {
	c.mu.RLock()
	conn, exists := c.clients[service.Addr]
	c.mu.RUnlock()
	if exists {
		return conn, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if conn, exists = c.clients[service.Addr]; exists {
		return conn, nil
	}

	dialOpts := make([]grpc.DialOption, len(c.dialOpts))
	copy(dialOpts, c.dialOpts)
	if len(c.unaryInterceptors) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainUnaryInterceptor(c.unaryInterceptors...))
	}
	if len(c.streamInterceptors) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainStreamInterceptor(c.streamInterceptors...))
	}
	// 注入 retry policy：使用调用方指定的策略，未指定则用默认策略。
	// 若 RetryableStatusCodes 为空表示主动禁用，不注入。
	rp := DefaultRetryPolicy()
	if c.retryPolicy != nil {
		rp = *c.retryPolicy
	}
	if len(rp.RetryableStatusCodes) > 0 {
		dialOpts = append(dialOpts, grpc.WithDefaultServiceConfig(rp.serviceConfig()))
	}

	conn, err := grpc.NewClient(service.Addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", service.Addr, err)
	}
	c.clients[service.Addr] = conn
	slog.Info("connected to service",
		"service", c.serviceName,
		"addr", service.Addr,
		"region", service.Metadata["region"],
		"zone", service.Metadata["zone"])
	return conn, nil
}

// refreshServices 从服务发现拉取并缓存服务列表
func (c *ServiceDiscoveryClient) refreshServices(ctx context.Context) error {
	services, err := c.discovery.Find(ctx, c.serviceName)
	if err != nil {
		return err
	}
	filtered := c.filterServices(services)
	c.mu.Lock()
	c.services = filtered
	c.wrrServices = nil // 触发权重表重建
	c.mu.Unlock()
	return nil
}

func (c *ServiceDiscoveryClient) filterServices(services []discover.ServiceInfo) []discover.ServiceInfo {
	if c.labelFilter == nil {
		return services
	}
	return c.labelFilter.Filter(services)
}

// WatchServices 监听服务变化，自动更新缓存并关闭失效连接。
// 回调处理在独立 goroutine 中执行，避免阻塞底层 watcher 事件循环。
func (c *ServiceDiscoveryClient) WatchServices(ctx context.Context) error {
	notifyCh := make(chan []discover.ServiceInfo, 1)

	c.bgWg.Add(1)
	go func() {
		defer c.bgWg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case services, ok := <-notifyCh:
				if !ok {
					return
				}
				c.applyServiceUpdate(services)
			}
		}
	}()

	return c.discovery.Watch(ctx, c.serviceName, func(services []discover.ServiceInfo) {
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

func (c *ServiceDiscoveryClient) applyServiceUpdate(services []discover.ServiceInfo) {
	filtered := c.filterServices(services)

	c.mu.Lock()
	c.services = filtered
	c.wrrServices = nil

	activeAddrs := make(map[string]bool, len(filtered))
	for _, s := range filtered {
		activeAddrs[s.Addr] = true
	}
	var toClose []*grpc.ClientConn
	for addr, conn := range c.clients {
		if !activeAddrs[addr] {
			toClose = append(toClose, conn)
			delete(c.clients, addr)
		}
	}
	c.mu.Unlock()

	for _, conn := range toClose {
		conn := conn
		if c.drainTimeout <= 0 {
			slog.Info("closing connection to removed service",
				"service", c.serviceName, "addr", conn.Target())
			conn.Close()
		} else {
			slog.Info("draining connection to removed service",
				"service", c.serviceName, "addr", conn.Target(),
				"drain_timeout", c.drainTimeout)
			go func() {
				time.Sleep(c.drainTimeout)
				conn.Close()
			}()
		}
	}

	slog.Info("service instances updated",
		"service", c.serviceName,
		"instances", len(filtered))
}

// healthCheckLoop 定期检查连接健康状态
func (c *ServiceDiscoveryClient) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(c.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			for addr, conn := range c.clients {
				if conn.GetState() == connectivity.Shutdown {
					slog.Warn("unhealthy connection detected, removing",
						"service", c.serviceName,
						"addr", addr)
					conn.Close()
					delete(c.clients, addr)
				}
			}
			c.mu.Unlock()
		}
	}
}

// GetServiceInfo 获取当前缓存的服务列表
func (c *ServiceDiscoveryClient) GetServiceInfo() []discover.ServiceInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.services
}

// Close 关闭所有连接
func (c *ServiceDiscoveryClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var lastErr error
	for addr, conn := range c.clients {
		if err := conn.Close(); err != nil {
			slog.Error("failed to close connection",
				"service", c.serviceName,
				"addr", addr,
				"error", err)
			lastErr = err
		}
	}
	c.clients = make(map[string]*grpc.ClientConn)
	return lastErr
}
