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

type Notify func([]ServiceInfo)
