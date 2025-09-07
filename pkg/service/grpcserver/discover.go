package grpcserver

import (
	"context"
	"fmt"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/uuid"
	"google.golang.org/grpc"
)

// ServiceDiscovery 从gRPC Server中读取已注册的服务
type ServiceDiscovery struct {
	server     *grpc.Server
	registries []discover.Registry
	services   map[string]*ProtoServiceInfo
}

// ProtoServiceInfo protobuf服务信息
type ProtoServiceInfo struct {
	ServiceName string            `json:"service_name"`
	Methods     []string          `json:"methods"`
	Metadata    map[string]string `json:"metadata"`
	ServerAddr  string            `json:"server_addr"`
}

// NewServiceDiscovery 创建服务发现器
func NewServiceDiscovery(server *grpc.Server, registries ...discover.Registry) *ServiceDiscovery {
	return &ServiceDiscovery{
		server:     server,
		registries: registries,
		services:   make(map[string]*ProtoServiceInfo),
	}
}

// DiscoverServices 发现gRPC Server中已注册的服务
func (sd *ServiceDiscovery) DiscoverServices(serverAddr string, baseMetadata map[string]string) error {
	// 使用gRPC内置的GetServiceInfo方法获取服务信息
	serviceInfos := sd.server.GetServiceInfo()

	for serviceName, serviceInfo := range serviceInfos {
		methods := make([]string, 0, len(serviceInfo.Methods))
		for _, method := range serviceInfo.Methods {
			methods = append(methods, method.Name)
		}

		// 合并元数据
		metadata := make(map[string]string)
		for k, v := range baseMetadata {
			metadata[k] = v
		}
		metadata["kind"] = "grpc"
		metadata["methods"] = fmt.Sprintf("%v", methods)
		if serviceInfo.Metadata != nil {
			metadata["proto_file"] = serviceInfo.Metadata.(string) // 包含proto文件信息
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
			"proto_file", serviceInfo.Metadata)
	}

	return nil
}

// RegisterAllServices 注册所有发现的服务
func (sd *ServiceDiscovery) RegisterAllServices(ctx context.Context) error {
	for serviceName, serviceInfo := range sd.services {
		go func(name string, info *ProtoServiceInfo) {
			// 为每个protobuf服务创建独立的注册实例
			serviceWrapper := &ProtoServiceWrapper{
				id:          uuid.New(),
				serviceName: name,
				methods:     info.Methods,
				addr:        info.ServerAddr,
				metadata:    info.Metadata,
			}

			// 注册到所有注册中心
			for _, registry := range sd.registries {
				go func(r discover.Registry) {
					stop, err := r.Register(ctx, serviceWrapper)
					if err != nil {
						logger.Error("proto service register error",
							"service", name,
							"error", err)
						return
					}
					defer stop()
					<-ctx.Done()
				}(registry)
			}
		}(serviceName, serviceInfo)
	}
	return nil
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
