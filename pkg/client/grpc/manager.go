package grpcclient

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// LoadBalanceStrategy 负载均衡策略
type LoadBalanceStrategy int

const (
	RoundRobin LoadBalanceStrategy = iota
	Random
	WeightedRoundRobin
	LeastConnections
)

// ClientManager 客户端管理器
type ClientManager struct {
	discovery          discover.Discovery
	serviceName        string
	clients            map[string]*grpc.ClientConn
	services           []discover.ServiceInfo
	mu                 sync.RWMutex
	dialOpts           []grpc.DialOption
	unaryInterceptors  []grpc.UnaryClientInterceptor
	streamInterceptors []grpc.StreamClientInterceptor
	// 负载均衡相关
	strategy     LoadBalanceStrategy
	currentIndex int
	// 标签过滤器
	labelFilter *ServiceLabelFilter
	// 健康检查
	healthCheck   bool
	checkInterval time.Duration
	// 故障转移
	failover   bool
	maxRetries int
	retryDelay time.Duration
}

// ClientManagerOption 客户端管理器选项
type ClientManagerOption func(*ClientManager)

// NewClientManager 创建客户端管理器
func NewClientManager(discovery discover.Discovery, serviceName string, opts ...ClientManagerOption) *ClientManager {
	manager := &ClientManager{
		discovery:   discovery,
		serviceName: serviceName,
		clients:     make(map[string]*grpc.ClientConn),
		dialOpts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                time.Second * 20,
				Timeout:             time.Second * 10,
				PermitWithoutStream: true,
			}),
			grpc.WithIdleTimeout(time.Second * 10),
		},
		strategy:      RoundRobin,
		healthCheck:   true,
		checkInterval: time.Second * 30,
		failover:      true,
		maxRetries:    3,
		retryDelay:    time.Second,
	}

	for _, opt := range opts {
		opt(manager)
	}

	return manager
}

// WithLoadBalanceStrategy 设置负载均衡策略
func WithLoadBalanceStrategy(strategy LoadBalanceStrategy) ClientManagerOption {
	return func(m *ClientManager) {
		m.strategy = strategy
	}
}

// WithManagerRegionFilter 设置地域过滤（支持多选）- 向后兼容方法
func WithManagerRegionFilter(regions, zones, campuses, environments []string) ClientManagerOption {
	return func(m *ClientManager) {
		m.labelFilter = NewLabelFilter().
			WithRegionIn(regions...).
			WithZoneIn(zones...).
			WithCampusIn(campuses...).
			WithEnvironmentIn(environments...)
	}
}

// WithManagerLabelFilter 设置标签过滤器
func WithManagerLabelFilter(filter *ServiceLabelFilter) ClientManagerOption {
	return func(m *ClientManager) {
		m.labelFilter = filter
	}
}

// WithManagerDialOptions 设置连接选项
func WithManagerDialOptions(opts ...grpc.DialOption) ClientManagerOption {
	return func(m *ClientManager) {
		m.dialOpts = append(m.dialOpts, opts...)
	}
}

// WithHealthCheck 设置健康检查
func WithHealthCheck(enabled bool, interval time.Duration) ClientManagerOption {
	return func(m *ClientManager) {
		m.healthCheck = enabled
		m.checkInterval = interval
	}
}

// WithFailover 设置故障转移
func WithFailover(enabled bool, maxRetries int, retryDelay time.Duration) ClientManagerOption {
	return func(m *ClientManager) {
		m.failover = enabled
		m.maxRetries = maxRetries
		m.retryDelay = retryDelay
	}
}

// Start 启动客户端管理器
func (m *ClientManager) Start(ctx context.Context) error {
	// 初始发现服务
	if err := m.refreshServices(ctx); err != nil {
		return fmt.Errorf("failed to refresh services: %w", err)
	}

	// 启动服务监听
	go m.watchServices(ctx)

	// 启动健康检查
	if m.healthCheck {
		go m.healthCheckLoop(ctx)
	}

	return nil
}

// GetClient 获取客户端连接
func (m *ClientManager) GetClient(ctx context.Context) (*grpc.ClientConn, error) {
	m.mu.RLock()
	services := m.services
	m.mu.RUnlock()

	if len(services) == 0 {
		return nil, fmt.Errorf("no services available for %s", m.serviceName)
	}

	// 选择服务实例
	service := m.selectService(services)
	if service == nil {
		return nil, fmt.Errorf("no suitable service found for %s", m.serviceName)
	}

	// 获取或创建连接
	return m.getOrCreateConnection(ctx, service)
}

// Call 调用服务方法
func (m *ClientManager) Call(ctx context.Context, method string, req, resp interface{}, opts ...grpc.CallOption) error {
	if !m.failover {
		// 不使用故障转移，直接调用
		conn, err := m.GetClient(ctx)
		if err != nil {
			return err
		}
		return conn.Invoke(ctx, method, req, resp, opts...)
	}

	// 使用故障转移
	var lastErr error
	for i := 0; i < m.maxRetries; i++ {
		conn, err := m.GetClient(ctx)
		if err != nil {
			lastErr = err
			continue
		}

		err = conn.Invoke(ctx, method, req, resp, opts...)
		if err == nil {
			return nil
		}

		lastErr = err

		// 等待重试
		if i < m.maxRetries-1 {
			time.Sleep(m.retryDelay)
		}
	}

	return fmt.Errorf("failed after %d retries: %w", m.maxRetries, lastErr)
}

// selectService 选择服务实例
func (m *ClientManager) selectService(services []discover.ServiceInfo) *discover.ServiceInfo {
	if len(services) == 0 {
		return nil
	}

	switch m.strategy {
	case RoundRobin:
		m.currentIndex = (m.currentIndex + 1) % len(services)
		return &services[m.currentIndex]
	case Random:
		index := rand.Intn(len(services))
		return &services[index]
	case WeightedRoundRobin:
		// 简单的权重轮询实现
		totalWeight := 0
		for _, service := range services {
			weight := 100 // 默认权重
			if w, ok := service.Metadata["weight"]; ok {
				// 解析权重
				if parsedWeight, err := strconv.Atoi(w); err == nil {
					weight = parsedWeight
				}
			}
			totalWeight += weight
		}

		// 选择权重最高的服务
		bestService := &services[0]
		bestWeight := 0
		for _, service := range services {
			weight := 100
			if w, ok := service.Metadata["weight"]; ok {
				// 解析权重
				if parsedWeight, err := strconv.Atoi(w); err == nil {
					weight = parsedWeight
				}
			}
			if weight > bestWeight {
				bestWeight = weight
				bestService = &service
			}
		}
		return bestService
	case LeastConnections:
		// 选择连接数最少的服务
		// 这里简化实现，实际应该跟踪连接数
		return &services[0]
	default:
		return &services[0]
	}
}

// getOrCreateConnection 获取或创建连接
func (m *ClientManager) getOrCreateConnection(ctx context.Context, service *discover.ServiceInfo) (*grpc.ClientConn, error) {
	m.mu.RLock()
	conn, exists := m.clients[service.Addr]
	m.mu.RUnlock()

	if exists {
		return conn, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 双重检查
	if conn, exists := m.clients[service.Addr]; exists {
		return conn, nil
	}

	// 创建连接
	dialOpts := make([]grpc.DialOption, len(m.dialOpts))
	copy(dialOpts, m.dialOpts)

	// 添加拦截器
	if len(m.unaryInterceptors) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainUnaryInterceptor(m.unaryInterceptors...))
	}
	if len(m.streamInterceptors) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainStreamInterceptor(m.streamInterceptors...))
	}

	conn, err := grpc.NewClient(service.Addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", service.Addr, err)
	}

	m.clients[service.Addr] = conn

	logger.Info("connected to service",
		"service", m.serviceName,
		"addr", service.Addr,
		"region", service.Metadata["region"],
		"zone", service.Metadata["zone"])

	return conn, nil
}

// refreshServices 刷新服务列表
func (m *ClientManager) refreshServices(ctx context.Context) error {
	services, err := m.discovery.Find(ctx, m.serviceName)
	if err != nil {
		return err
	}

	// 过滤服务
	filteredServices := m.filterServices(services)

	m.mu.Lock()
	m.services = filteredServices
	m.mu.Unlock()

	logger.Info("refreshed services",
		"service", m.serviceName,
		"count", len(filteredServices))

	return nil
}

// filterServices 根据标签过滤器过滤服务实例
func (m *ClientManager) filterServices(services []discover.ServiceInfo) []discover.ServiceInfo {
	if m.labelFilter == nil {
		return services
	}
	return m.labelFilter.Filter(services)
}

// watchServices 监听服务变化
func (m *ClientManager) watchServices(ctx context.Context) {
	err := m.discovery.Watch(ctx, m.serviceName, func(services []discover.ServiceInfo) {
		m.mu.Lock()
		defer m.mu.Unlock()

		// 更新服务列表
		filteredServices := m.filterServices(services)
		m.services = filteredServices

		// 清理无效连接
		activeAddrs := make(map[string]bool)
		for _, service := range filteredServices {
			activeAddrs[service.Addr] = true
		}

		for addr, conn := range m.clients {
			if !activeAddrs[addr] {
				logger.Info("closing connection to removed service",
					"service", m.serviceName,
					"addr", addr)
				conn.Close()
				delete(m.clients, addr)
			}
		}

		logger.Info("services updated",
			"service", m.serviceName,
			"count", len(filteredServices))
	})

	if err != nil {
		logger.Error("watch services failed",
			"service", m.serviceName,
			"error", err)
	}
}

// healthCheckLoop 健康检查循环
func (m *ClientManager) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.performHealthCheck(ctx)
		}
	}
}

// performHealthCheck 执行健康检查
func (m *ClientManager) performHealthCheck(ctx context.Context) {
	m.mu.RLock()
	clients := make(map[string]*grpc.ClientConn)
	for addr, conn := range m.clients {
		clients[addr] = conn
	}
	m.mu.RUnlock()

	for addr, conn := range clients {
		// 简单的健康检查：尝试获取连接状态
		state := conn.GetState()
		if state.String() == "SHUTDOWN" {
			logger.Warn("unhealthy connection detected",
				"service", m.serviceName,
				"addr", addr,
				"state", state)

			// 关闭不健康的连接
			m.mu.Lock()
			if conn, exists := m.clients[addr]; exists {
				conn.Close()
				delete(m.clients, addr)
			}
			m.mu.Unlock()
		}
	}
}

// Close 关闭所有连接
func (m *ClientManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for addr, conn := range m.clients {
		if err := conn.Close(); err != nil {
			logger.Error("failed to close connection",
				"service", m.serviceName,
				"addr", addr,
				"error", err)
			lastErr = err
		}
	}

	m.clients = make(map[string]*grpc.ClientConn)
	return lastErr
}
