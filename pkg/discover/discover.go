package discover

import (
	"context"
)

type Registry interface {
	Register(context.Context, Service) (context.CancelFunc, error)
}

// type KeepAlive interface {
// 	KeepAlive() error
// 	Stop() error
// }

type Discovery interface {
	Find(ctx context.Context, serviceName string) ([]ServiceInfo, error)
	Watch(ctx context.Context, serviceName string, n Notify) error
}

type Notify func([]ServiceInfo)

// type Watcher interface {
// 	Next() <-chan map[string]ServiceInfo
// 	Close() error
// }

// type watch struct {
// 	ch chan map[string]ServiceInfo
// }

// func (w watch) Next() <-chan map[string]ServiceInfo {
// 	return w.ch
// }

// func (w watch) Close() error {
// 	close(w.ch)
// 	return nil
// }
