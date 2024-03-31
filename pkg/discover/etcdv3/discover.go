package etcdv3

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func (r EtcdRegistry) Find(ctx context.Context, name string) ([]discover.ServiceInfo, error) {
	var services []discover.ServiceInfo
	prefix := fmt.Sprintf("/%s/%s", r.namespace, name)
	resp, err := r.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return services, err
	}
	for _, kv := range resp.Kvs {
		// /beauty/helloworld.rpc/6bf14822-755d-4571-a7f5-bfe336783742
		instanceID := getInstanceFromKey(string(kv.Key), prefix)
		v := discover.ServiceInfo{}
		v.Unmarshal(kv.Value)
		v.ID = instanceID
		services = append(services, v)
	}
	return services, nil
}

func getInstanceFromKey(key, prefix string) string {
	return strings.TrimPrefix(strings.ReplaceAll(string(key), prefix, ""), "/")
}

func (r EtcdRegistry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	prefix := fmt.Sprintf("/%s/%s", r.namespace, serviceName)
	var endpoints = make(map[string]discover.ServiceInfo)
	resp, err := r.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}
	for _, kv := range resp.Kvs {
		v := discover.ServiceInfo{}
		v.Unmarshal(kv.Value)
		v.ID = getInstanceFromKey(string(kv.Key), prefix)
		endpoints[v.ID] = v
	}
	var services []discover.ServiceInfo
	for _, v := range endpoints {
		services = append(services, v)
	}
	update(services)
	// fmt.Println("resp.Header.Revision", resp.Header.Revision, key)
	w := clientv3.NewWatcher(r.client)
	ch := w.Watch(ctx, prefix,
		clientv3.WithPrefix(),
		clientv3.WithRev(resp.Header.Revision),
	)
	if err := w.RequestProgress(ctx); err != nil {
		logger.Error("watch Progress error", slog.Any("err", err))
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, closed := <-ch:
			if closed {
				logger.Error("watch closed")
				return nil
			}
			if event.Canceled {
				logger.Error("watch event canceled", slog.Any("err", event.Err()))
				return nil
			}
			// if event.IsProgressNotify() {
			// 	fmt.Println("watch ProgressNotify")
			// 	continue
			// }
			// time.Sleep(time.Second * 1)
			// fmt.Println("watch >>>>>", event, event.Events, event.Header.Revision)
			for _, ev := range event.Events {
				// fmt.Println(">>", key, ev.Type)
				key := string(ev.Kv.Key)
				// ev.IsCreate()
				// instanceID := getInstanceFromKey(key, prefix)
				switch ev.Type {
				case clientv3.EventTypePut:
					v := discover.ServiceInfo{}
					v.Unmarshal(ev.Kv.Value)
					v.ID = getInstanceFromKey(key, prefix)
					endpoints[key] = v
					var services []discover.ServiceInfo
					for _, v := range endpoints {
						services = append(services, v)
					}
					update(services)
				case clientv3.EventTypeDelete:
					delete(endpoints, key)
					var services []discover.ServiceInfo
					for _, v := range endpoints {
						services = append(services, v)
					}
					update(services)
				}
			}
		}
	}
}
