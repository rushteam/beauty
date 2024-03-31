package discover

import (
	"context"
)

// type baseResolver struct{}

// func (baseResolver) ResolveNow(resolver.ResolveNowOptions) {}

// func (baseResolver) Close() {}

type Discovery interface {
	Find(ctx context.Context, serviceName string) ([]ServiceInfo, error)
	Watch(ctx context.Context, serviceName string, n Notify) error
}

type Notify func([]ServiceInfo)

// type Watcher interface {
// 	Next(map[string]ServiceInfo) error
// 	Close()
// 	Done() <-chan struct{}
// }

// func (w Watcher) Next() []ServiceInfo

// func (w *Watcher) Next() ([]ServiceInfo, error) {
// 	endpoints, err := w.d.Find(w.ctx, w.serviceName)
// 	if err != nil {

// 	}
// 	select {
// 	case <-w.ctx.Done():
// 		return w.endpoints, w.ctx.Err()
// 	case event := <-w.ch:
// 		if event.Canceled {
// 			log.Printf("failed to watch server addresses changed, caused by: %v", event.Err())
// 			return w.endpoints, w.ctx.Err()
// 		}
// 		for _, ev := range event.Events {
// 			key := string(ev.Kv.Key)
// 			// ev.IsCreate()
// 			switch ev.Type {
// 			case clientv3.EventTypePut:
// 				v := discover.ServiceInfo{}
// 				v.Unmarshal(ev.Kv.Value)
// 				w.endpoints[key] = &v
// 			case clientv3.EventTypeDelete:
// 				delete(w.endpoints, key)
// 			}
// 		}
// 	}
// 	return w.endpoints, nil
// }
