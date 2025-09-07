package grpcclient

import (
	"context"
	"sync"

	"github.com/rushteam/beauty/pkg/service/discover"
	"google.golang.org/grpc"
)

// ClientFactory gRPC客户端工厂
type ClientFactory struct {
	discovery   discover.Discovery
	clients     map[string]*ServiceDiscoveryClient
	mu          sync.RWMutex
	defaultOpts []ServiceDiscoveryOption
}

// NewClientFactory 创建客户端工厂
func NewClientFactory(discovery discover.Discovery, defaultOpts ...ServiceDiscoveryOption) *ClientFactory {
	return &ClientFactory{
		discovery:   discovery,
		clients:     make(map[string]*ServiceDiscoveryClient),
		defaultOpts: defaultOpts,
	}
}

// GetClient 获取指定服务的客户端
func (f *ClientFactory) GetClient(serviceName string, opts ...ServiceDiscoveryOption) *ServiceDiscoveryClient {
	f.mu.RLock()
	client, exists := f.clients[serviceName]
	f.mu.RUnlock()

	if exists {
		return client
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// 双重检查
	if client, exists := f.clients[serviceName]; exists {
		return client
	}

	// 合并默认选项和用户选项
	allOpts := make([]ServiceDiscoveryOption, 0, len(f.defaultOpts)+len(opts))
	allOpts = append(allOpts, f.defaultOpts...)
	allOpts = append(allOpts, opts...)

	// 创建新客户端
	client = NewServiceDiscoveryClient(f.discovery, serviceName, allOpts...)
	f.clients[serviceName] = client

	return client
}

// Close 关闭所有客户端
func (f *ClientFactory) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	var lastErr error
	for serviceName, client := range f.clients {
		if err := client.Close(); err != nil {
			lastErr = err
		}
		delete(f.clients, serviceName)
	}

	return lastErr
}

// WatchAllServices 监听所有服务的变化
func (f *ClientFactory) WatchAllServices(ctx context.Context) error {
	f.mu.RLock()
	services := make([]string, 0, len(f.clients))
	for serviceName := range f.clients {
		services = append(services, serviceName)
	}
	f.mu.RUnlock()

	// 为每个服务启动监听
	for _, serviceName := range services {
		go func(name string) {
			client := f.GetClient(name)
			if err := client.WatchServices(ctx); err != nil {
				// 记录错误但不返回，让其他服务继续监听
			}
		}(serviceName)
	}

	return nil
}

// ServiceClient 特定服务的客户端包装器
type ServiceClient struct {
	*ServiceDiscoveryClient
	serviceName string
}

// NewServiceClient 创建特定服务的客户端
func NewServiceClient(discovery discover.Discovery, serviceName string, opts ...ServiceDiscoveryOption) *ServiceClient {
	return &ServiceClient{
		ServiceDiscoveryClient: NewServiceDiscoveryClient(discovery, serviceName, opts...),
		serviceName:            serviceName,
	}
}

// Call 调用服务方法
func (c *ServiceClient) Call(ctx context.Context, method string, req, resp interface{}, opts ...grpc.CallOption) error {
	conn, err := c.GetClient(ctx)
	if err != nil {
		return err
	}

	return conn.Invoke(ctx, method, req, resp, opts...)
}

// NewStream 创建流
func (c *ServiceClient) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	conn, err := c.GetClient(ctx)
	if err != nil {
		return nil, err
	}

	return conn.NewStream(ctx, desc, method, opts...)
}

// GetDiscovery 获取服务发现实例
func (f *ClientFactory) GetDiscovery() discover.Discovery {
	return f.discovery
}
