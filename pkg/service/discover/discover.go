package discover

import (
	"context"
)

type Registry interface {
	Register(context.Context, Service) (context.CancelFunc, error)
}

type Discovery interface {
	Find(ctx context.Context, serviceName string) ([]ServiceInfo, error)
	Watch(ctx context.Context, serviceName string, n Notify) error
}

// RegistryDiscovery 同时具备服务注册和发现能力，大多数注册中心均实现此接口。
type RegistryDiscovery interface {
	Registry
	Discovery
}

type Notify func([]ServiceInfo)
