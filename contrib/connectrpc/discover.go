package connectrpc

import (
	"context"
	"maps"
	"sync"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/uuid"
)

var internalServices = map[string]bool{
	"grpc.health.v1.Health": true,
}

// ServiceDiscovery 从 Connect Server 中已注册的 handler 收集 protobuf 服务名，
// 并逐个注册到注册中心（与 grpcserver.ServiceDiscovery 对等）。
type ServiceDiscovery struct {
	registries      []discover.Registry
	services        map[string]*ProtoServiceInfo
	includeInternal bool
}

// ProtoServiceInfo 记录单个 protobuf 服务的注册信息。
type ProtoServiceInfo struct {
	ServiceName string
	Metadata    map[string]string
	ServerAddr  string
}

// ServiceDiscoveryOption 服务发现选项。
type ServiceDiscoveryOption func(*ServiceDiscovery)

// WithInternalServices 将健康检查等内部服务也注册到注册中心。
func WithInternalServices() ServiceDiscoveryOption {
	return func(sd *ServiceDiscovery) {
		sd.includeInternal = true
	}
}

// NewServiceDiscovery 创建服务发现器。
func NewServiceDiscovery(registries []discover.Registry, opts ...ServiceDiscoveryOption) *ServiceDiscovery {
	sd := &ServiceDiscovery{
		registries: registries,
		services:   make(map[string]*ProtoServiceInfo),
	}
	for _, o := range opts {
		o(sd)
	}
	return sd
}

// DiscoverServices 根据 Handle 注册的服务名生成注册信息。
func (sd *ServiceDiscovery) DiscoverServices(serverAddr string, baseMetadata map[string]string, serviceNames []string) {
	for _, serviceName := range serviceNames {
		if !sd.includeInternal && internalServices[serviceName] {
			continue
		}

		metadata := make(map[string]string)
		maps.Copy(metadata, baseMetadata)
		metadata["kind"] = "connect"

		defaults := map[string]string{
			"region":      "default",
			"zone":        "default",
			"campus":      "default",
			"environment": "production",
			"weight":      "100",
			"priority":    "0",
		}
		for k, v := range defaults {
			if metadata[k] == "" {
				metadata[k] = v
			}
		}

		sd.services[serviceName] = &ProtoServiceInfo{
			ServiceName: serviceName,
			Metadata:    metadata,
			ServerAddr:  serverAddr,
		}

		logger.Info("discovered connect service",
			"service", serviceName,
			"addr", serverAddr,
			"region", metadata["region"],
			"zone", metadata["zone"],
			"environment", metadata["environment"],
			"weight", metadata["weight"])
	}
}

// RegisterAllServices 注册所有发现的服务，返回一个 wait 函数，
// 调用方可在 ctx 取消后调用 wait() 确保所有注册 goroutine 已退出。
func (sd *ServiceDiscovery) RegisterAllServices(ctx context.Context) (wait func(), err error) {
	var wg sync.WaitGroup
	for serviceName, serviceInfo := range sd.services {
		wrapper := &protoServiceWrapper{
			id:          uuid.New(),
			serviceName: serviceName,
			addr:        serviceInfo.ServerAddr,
			metadata:    serviceInfo.Metadata,
		}
		for _, registry := range sd.registries {
			wg.Add(1)
			go func(r discover.Registry, name string) {
				defer wg.Done()
				stop, err := r.Register(ctx, wrapper)
				if err != nil {
					logger.Error("connect service register error",
						"service", name,
						"error", err)
					return
				}
				defer stop()
				<-ctx.Done()
			}(registry, serviceName)
		}
	}
	return wg.Wait, nil
}

// protoServiceWrapper 实现 discover.Service 接口。
type protoServiceWrapper struct {
	id          string
	serviceName string
	addr        string
	metadata    map[string]string
}

func (w *protoServiceWrapper) ID() string                  { return w.id }
func (w *protoServiceWrapper) Name() string                { return w.serviceName }
func (w *protoServiceWrapper) Kind() string                { return "connect" }
func (w *protoServiceWrapper) Addr() string                { return w.addr }
func (w *protoServiceWrapper) Metadata() map[string]string { return w.metadata }
