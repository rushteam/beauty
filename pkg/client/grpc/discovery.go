package grpcclient

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// ServiceDiscoveryClient 基于服务发现的gRPC客户端
type ServiceDiscoveryClient struct {
	discovery          discover.Discovery
	serviceName        string
	clients            map[string]*grpc.ClientConn // 缓存连接
	mu                 sync.RWMutex
	dialOpts           []grpc.DialOption
	unaryInterceptors  []grpc.UnaryClientInterceptor
	streamInterceptors []grpc.StreamClientInterceptor
	// 标签过滤器
	labelFilter *ServiceLabelFilter
}

// ServiceDiscoveryOption 服务发现客户端选项
type ServiceDiscoveryOption func(*ServiceDiscoveryClient)

// NewServiceDiscoveryClient 创建基于服务发现的客户端
func NewServiceDiscoveryClient(discovery discover.Discovery, serviceName string, opts ...ServiceDiscoveryOption) *ServiceDiscoveryClient {
	client := &ServiceDiscoveryClient{
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
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// WithDiscoveryRegionFilter 设置地域过滤（支持多选）- 向后兼容方法
func WithDiscoveryRegionFilter(regions, zones, campuses, environments []string) ServiceDiscoveryOption {
	return func(c *ServiceDiscoveryClient) {
		c.labelFilter = NewLabelFilter().
			WithRegionIn(regions...).
			WithZoneIn(zones...).
			WithCampusIn(campuses...).
			WithEnvironmentIn(environments...)
	}
}

// WithDiscoveryLabelFilter 设置标签过滤器
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

// GetClient 获取指定服务的客户端连接
func (c *ServiceDiscoveryClient) GetClient(ctx context.Context) (*grpc.ClientConn, error) {
	// 从服务发现获取服务实例
	services, err := c.discovery.Find(ctx, c.serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to find service %s: %w", c.serviceName, err)
	}

	if len(services) == 0 {
		return nil, fmt.Errorf("no instances found for service %s", c.serviceName)
	}

	// 过滤服务实例
	filteredServices := c.filterServices(services)
	if len(filteredServices) == 0 {
		return nil, fmt.Errorf("no instances found for service %s after filtering", c.serviceName)
	}

	// 选择第一个可用的服务实例
	service := filteredServices[0]

	c.mu.RLock()
	client, exists := c.clients[service.Addr]
	c.mu.RUnlock()

	if exists {
		return client, nil
	}

	// 创建新连接
	c.mu.Lock()
	defer c.mu.Unlock()

	// 双重检查
	if client, exists := c.clients[service.Addr]; exists {
		return client, nil
	}

	// 构建连接选项
	dialOpts := make([]grpc.DialOption, len(c.dialOpts))
	copy(dialOpts, c.dialOpts)

	// 添加拦截器
	if len(c.unaryInterceptors) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainUnaryInterceptor(c.unaryInterceptors...))
	}
	if len(c.streamInterceptors) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainStreamInterceptor(c.streamInterceptors...))
	}

	// 建立连接
	conn, err := grpc.NewClient(service.Addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", service.Addr, err)
	}

	c.clients[service.Addr] = conn

	logger.Info("connected to service",
		"service", c.serviceName,
		"addr", service.Addr,
		"region", service.Metadata["region"],
		"zone", service.Metadata["zone"],
		"environment", service.Metadata["environment"])

	return conn, nil
}

// filterServices 根据标签过滤器过滤服务实例
func (c *ServiceDiscoveryClient) filterServices(services []discover.ServiceInfo) []discover.ServiceInfo {
	if c.labelFilter == nil {
		return services
	}
	return c.labelFilter.Filter(services)
}

// contains 检查字符串切片是否包含指定值
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// WatchServices 监听服务变化
func (c *ServiceDiscoveryClient) WatchServices(ctx context.Context) error {
	return c.discovery.Watch(ctx, c.serviceName, func(services []discover.ServiceInfo) {
		c.mu.Lock()
		defer c.mu.Unlock()

		// 获取当前活跃的服务地址
		activeAddrs := make(map[string]bool)
		for _, service := range services {
			activeAddrs[service.Addr] = true
		}

		// 关闭不再活跃的连接
		for addr, conn := range c.clients {
			if !activeAddrs[addr] {
				logger.Info("closing connection to removed service",
					"service", c.serviceName,
					"addr", addr)
				conn.Close()
				delete(c.clients, addr)
			}
		}

		logger.Info("service instances updated",
			"service", c.serviceName,
			"instances", len(services))
	})
}

// Close 关闭所有连接
func (c *ServiceDiscoveryClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var lastErr error
	for addr, conn := range c.clients {
		if err := conn.Close(); err != nil {
			logger.Error("failed to close connection",
				"service", c.serviceName,
				"addr", addr,
				"error", err)
			lastErr = err
		}
	}

	c.clients = make(map[string]*grpc.ClientConn)
	return lastErr
}

// GetServiceInfo 获取服务信息
func (c *ServiceDiscoveryClient) GetServiceInfo(ctx context.Context) ([]discover.ServiceInfo, error) {
	return c.discovery.Find(ctx, c.serviceName)
}
