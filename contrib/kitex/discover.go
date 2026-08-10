package kitex

import (
	"context"
	"fmt"
	"maps"
	"sync"

	kserver "github.com/cloudwego/kitex/server"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/uuid"
)

// ServiceDiscovery 从 Kitex Server 中读取已注册的 Thrift 服务，
// 并逐个注册到 beauty 注册中心。
type ServiceDiscovery struct {
	registries []discover.Registry
	services   map[string]*ThriftServiceInfo
}

// ThriftServiceInfo 记录单个 Thrift 服务的注册信息。
type ThriftServiceInfo struct {
	ServiceName string
	Methods     []string
	Metadata    map[string]string
	ServerAddr  string
}

// ServiceDiscoveryOption 服务发现选项。
type ServiceDiscoveryOption func(*ServiceDiscovery)

// NewServiceDiscovery 创建服务发现器。
func NewServiceDiscovery(registries []discover.Registry, opts ...ServiceDiscoveryOption) *ServiceDiscovery {
	sd := &ServiceDiscovery{
		registries: registries,
		services:   make(map[string]*ThriftServiceInfo),
	}
	for _, o := range opts {
		o(sd)
	}
	return sd
}

// DiscoverServices 从 Kitex Server 中发现已注册的 Thrift 服务。
func (sd *ServiceDiscovery) DiscoverServices(svr kserver.Server, serverAddr string, baseMetadata map[string]string) {
	serviceInfos := svr.GetServiceInfos()
	for serviceName, serviceInfo := range serviceInfos {
		methods := make([]string, 0, len(serviceInfo.Methods))
		for methodName := range serviceInfo.Methods {
			methods = append(methods, methodName)
		}

		metadata := make(map[string]string)
		maps.Copy(metadata, baseMetadata)
		metadata["kind"] = "thrift"
		metadata["methods"] = fmt.Sprintf("%v", methods)

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

		sd.services[serviceName] = &ThriftServiceInfo{
			ServiceName: serviceName,
			Methods:     methods,
			Metadata:    metadata,
			ServerAddr:  serverAddr,
		}

		logger.Info("discovered kitex service",
			"service", serviceName,
			"methods", methods,
			"addr", serverAddr,
			"weight", metadata["weight"])
	}
}

// RegisterAllServices 注册所有发现的服务，返回一个 wait 函数，
// 调用方可在 ctx 取消后调用 wait() 确保所有注册 goroutine 已退出。
func (sd *ServiceDiscovery) RegisterAllServices(ctx context.Context) (wait func(), err error) {
	var wg sync.WaitGroup
	for serviceName, serviceInfo := range sd.services {
		wrapper := &thriftServiceWrapper{
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
					logger.Error("kitex service register error",
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

// thriftServiceWrapper 实现 discover.Service 接口。
type thriftServiceWrapper struct {
	id          string
	serviceName string
	addr        string
	metadata    map[string]string
}

func (w *thriftServiceWrapper) ID() string                  { return w.id }
func (w *thriftServiceWrapper) Name() string                { return w.serviceName }
func (w *thriftServiceWrapper) Kind() string                { return "thrift" }
func (w *thriftServiceWrapper) Addr() string                { return w.addr }
func (w *thriftServiceWrapper) Metadata() map[string]string { return w.metadata }
