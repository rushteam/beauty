package grpcserver

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/uuid"
	"google.golang.org/grpc"
)

// internalServices 是框架自动注册的内部服务，默认不暴露到服务注册中心
var internalServices = map[string]bool{
	"grpc.health.v1.Health":                    true,
	"grpc.reflection.v1.ServerReflection":      true,
	"grpc.reflection.v1alpha.ServerReflection": true,
}

// ServiceDiscovery 从gRPC Server中读取已注册的服务
type ServiceDiscovery struct {
	server          *grpc.Server
	registries      []discover.Registry
	services        map[string]*ProtoServiceInfo
	includeInternal bool
}

// ProtoServiceInfo protobuf服务信息
type ProtoServiceInfo struct {
	ServiceName string            `json:"service_name"`
	Methods     []string          `json:"methods"`
	Metadata    map[string]string `json:"metadata"`
	ServerAddr  string            `json:"server_addr"`
}

// ServiceDiscoveryOption 服务发现选项
type ServiceDiscoveryOption func(*ServiceDiscovery)

// WithInternalServices 将 health check、reflection 等内部服务也注册到服务注册中心
func WithInternalServices() ServiceDiscoveryOption {
	return func(sd *ServiceDiscovery) {
		sd.includeInternal = true
	}
}

// NewServiceDiscovery 创建服务发现器
func NewServiceDiscovery(server *grpc.Server, registries []discover.Registry, opts ...ServiceDiscoveryOption) *ServiceDiscovery {
	sd := &ServiceDiscovery{
		server:     server,
		registries: registries,
		services:   make(map[string]*ProtoServiceInfo),
	}
	for _, o := range opts {
		o(sd)
	}
	return sd
}

// DiscoverServices 发现gRPC Server中已注册的服务
func (sd *ServiceDiscovery) DiscoverServices(serverAddr string, baseMetadata map[string]string) error {
	// 使用gRPC内置的GetServiceInfo方法获取服务信息
	serviceInfos := sd.server.GetServiceInfo()

	for serviceName, serviceInfo := range serviceInfos {
		if !sd.includeInternal && internalServices[serviceName] {
			continue
		}
		methods := make([]string, 0, len(serviceInfo.Methods))
		for _, method := range serviceInfo.Methods {
			methods = append(methods, method.Name)
		}

		// 合并元数据，确保地域信息被正确传递
		metadata := make(map[string]string)
		maps.Copy(metadata, baseMetadata)

		// 设置基础元数据
		metadata["kind"] = "grpc"
		metadata["methods"] = fmt.Sprintf("%v", methods)
		if s, ok := serviceInfo.Metadata.(string); ok {
			metadata["proto_file"] = s
		}

		// 确保Polaris兼容的地域信息
		if metadata["region"] == "" {
			metadata["region"] = "default"
		}
		if metadata["zone"] == "" {
			metadata["zone"] = "default"
		}
		if metadata["campus"] == "" {
			metadata["campus"] = "default"
		}
		if metadata["environment"] == "" {
			metadata["environment"] = "production"
		}
		if metadata["weight"] == "" {
			metadata["weight"] = "100"
		}
		if metadata["priority"] == "" {
			metadata["priority"] = "0"
		}

		protoService := &ProtoServiceInfo{
			ServiceName: serviceName,
			Methods:     methods,
			Metadata:    metadata,
			ServerAddr:  serverAddr,
		}

		sd.services[serviceName] = protoService

		logger.Info("discovered gRPC service",
			"service", serviceName,
			"methods", methods,
			"region", metadata["region"],
			"zone", metadata["zone"],
			"environment", metadata["environment"],
			"weight", metadata["weight"],
			"proto_file", serviceInfo.Metadata)
	}

	return nil
}

// RegisterAllServices 注册所有发现的服务，返回一个 wait 函数，
// 调用方可在 ctx 取消后调用 wait() 确保所有注册 goroutine 已退出。
func (sd *ServiceDiscovery) RegisterAllServices(ctx context.Context) (wait func(), err error) {
	var wg sync.WaitGroup
	for serviceName, serviceInfo := range sd.services {
		serviceWrapper := &ProtoServiceWrapper{
			id:          uuid.New(),
			serviceName: serviceName,
			methods:     serviceInfo.Methods,
			addr:        serviceInfo.ServerAddr,
			metadata:    serviceInfo.Metadata,
		}
		for _, registry := range sd.registries {
			wg.Add(1)
			go func(r discover.Registry, name string) {
				defer wg.Done()
				stop, err := r.Register(ctx, serviceWrapper)
				if err != nil {
					logger.Error("proto service register error",
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

// ProtoServiceWrapper 实现discover.Service接口
type ProtoServiceWrapper struct {
	id          string
	serviceName string
	methods     []string
	addr        string
	metadata    map[string]string
}

func (w *ProtoServiceWrapper) ID() string {
	return w.id
}

func (w *ProtoServiceWrapper) Name() string {
	return w.serviceName
}

func (w *ProtoServiceWrapper) Kind() string {
	return "grpc"
}

func (w *ProtoServiceWrapper) Addr() string {
	return w.addr
}

func (w *ProtoServiceWrapper) Metadata() map[string]string {
	return w.metadata
}
