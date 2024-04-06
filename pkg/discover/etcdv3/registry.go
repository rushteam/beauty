package etcdv3

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var grantTTL int64 = 10

var instance = make(map[string]*Registry)
var mu sync.Mutex

var (
	_ discover.Registry  = (*Registry)(nil)
	_ discover.Discovery = (*Registry)(nil)
)

func NewRegistry(c *Config) *Registry {
	key := c.String()
	if v, ok := instance[key]; ok {
		return v
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   c.Endpoints,
		Username:    c.Username,
		Password:    c.Password,
		DialTimeout: time.Second * 3,
	})
	if err != nil {
		logger.Error("etcdRegistry client error", slog.Any("err", err))
		return nil
	}
	r := &Registry{
		client: client,
		prefix: c.Prefix,
		config: c,
	}
	mu.Lock()
	defer mu.Unlock()
	instance[key] = r
	return r
}

type Registry struct {
	config *Config
	client *clientv3.Client
	prefix string
	discover.Registry
	discover.Discovery
}

func (r Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	value := discover.ServiceInfo{
		Name:     info.Name(),
		Addr:     info.Addr(),
		Metadata: info.Metadata(),
	}
	key := buildServiceKey(r.prefix, info.Name(), info.ID())
	ctx, stop := context.WithCancel(ctx)
	go r.keepAlive(ctx, key, value.Marshal())
	return func() {
		stop()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		if _, err := r.client.Delete(ctx, key); err != nil {
			logger.Error("etcdRegistry.Deregister Delete error", slog.Any("err", err))
		}
		cancel()
	}, nil
}

func (r Registry) keepAlive(ctx context.Context, key, val string) {
	var leaseid clientv3.LeaseID
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second*3)
	if lease, err := r.client.Grant(timeoutCtx, grantTTL); err == nil {
		leaseid = lease.ID
	}
	if _, err := r.client.Put(timeoutCtx, key, val, clientv3.WithLease(leaseid)); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.Error("etcdRegistry.Register Put error", slog.Any("err", err))
		}
	}
	cancel()
	t := time.NewTicker(time.Second * time.Duration(grantTTL-2))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
				defer cancel()
				if _, err := r.client.Revoke(ctx, leaseid); err != nil {
					logger.Error("etcdRegistry.Register Revoke error", slog.Any("err", err))
				}
			}()
			return
		case <-t.C:
			func() {
				keepCtx, cancel := context.WithTimeout(ctx, time.Second*3)
				defer cancel()
				if _, err := r.client.KeepAliveOnce(keepCtx, leaseid); err != nil {
					grantCtx, cancel := context.WithTimeout(ctx, time.Second*3)
					defer cancel()
					lease, err := r.client.Grant(grantCtx, grantTTL)
					if err != nil {
						logger.Error("etcdRegistry.Register Grant error", slog.Any("err", err))
						return
					}
					leaseid = lease.ID
					putCtx, cancel := context.WithTimeout(ctx, time.Second*3)
					defer cancel()
					if _, err = r.client.Put(putCtx, key, val, clientv3.WithLease(leaseid)); err != nil {
						if !errors.Is(err, context.Canceled) {
							logger.Error("etcdRegistry.Register Put error", slog.Any("err", err))
						}
					}
				}
			}()
		}
	}
}

func buildServiceKey(prefix, name, id string) string {
	return fmt.Sprintf("/%s/%s/%s", strings.TrimLeft(prefix, "/"), name, id)
}

func buildServicePath(prefix, name string) string {
	return fmt.Sprintf("/%s/%s", strings.TrimLeft(prefix, "/"), name)
}

func getInstanceFromKey(key, prefix string) string {
	return strings.TrimPrefix(strings.ReplaceAll(string(key), prefix, ""), "/")
}

func (r Registry) Find(ctx context.Context, name string) ([]discover.ServiceInfo, error) {
	var services []discover.ServiceInfo
	path := buildServicePath(r.prefix, name)
	resp, err := r.client.Get(ctx, path, clientv3.WithPrefix())
	if err != nil {
		return services, err
	}
	for _, kv := range resp.Kvs {
		// /beauty/helloworld.rpc/6bf14822-755d-4571-a7f5-bfe336783742
		instanceID := getInstanceFromKey(string(kv.Key), path)
		v := discover.ServiceInfo{}
		v.Unmarshal(kv.Value)
		v.ID = instanceID
		services = append(services, v)
	}
	return services, nil
}

func (r Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	path := buildServicePath(r.prefix, serviceName)
	var endpoints = make(map[string]discover.ServiceInfo)
	resp, err := r.client.Get(ctx, path, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}
	for _, kv := range resp.Kvs {
		v := discover.ServiceInfo{}
		v.Unmarshal(kv.Value)
		v.ID = getInstanceFromKey(string(kv.Key), path)
		endpoints[v.ID] = v
	}
	var services []discover.ServiceInfo
	for _, v := range endpoints {
		services = append(services, v)
	}
	update(services)
	// fmt.Println("resp.Header.Revision", resp.Header.Revision, key)
	w := clientv3.NewWatcher(r.client)
	ch := w.Watch(ctx, path,
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
			for _, ev := range event.Events {
				key := string(ev.Kv.Key)
				switch ev.Type {
				case clientv3.EventTypePut:
					v := discover.ServiceInfo{}
					v.Unmarshal(ev.Kv.Value)
					v.ID = getInstanceFromKey(key, path)
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
