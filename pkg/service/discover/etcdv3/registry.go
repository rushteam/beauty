package etcdv3

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
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
	mu.Lock()
	if v, ok := instance[key]; ok {
		mu.Unlock()
		return v
	}
	mu.Unlock()
	dial := time.Second * 3
	if c.DialMS > 0 {
		dial = time.Duration(c.DialMS) * time.Millisecond
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   c.Endpoints,
		Username:    c.Username,
		Password:    c.Password,
		DialTimeout: dial,
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
	if v, ok := instance[key]; ok {
		mu.Unlock()
		_ = client.Close()
		return v
	}
	instance[key] = r
	mu.Unlock()
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
		ID:       info.ID(),
		Kind:     info.Kind(),
		Name:     info.Name(),
		Addr:     info.Addr(),
		Metadata: info.Metadata(),
	}
	key := buildServiceKey(r.prefix, info.Name(), info.ID())

	// 同步首次注册（带重试）
	leaseID, err := r.registerWithRetry(ctx, key, value.Marshal())
	if err != nil {
		return func() {}, err
	}

	regCtx, stop := context.WithCancel(ctx)
	go r.keepAliveLoop(regCtx, key, value.Marshal(), leaseID)
	return func() {
		stop()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		if _, err := r.client.Delete(ctx, key); err != nil {
			logger.Error("etcdRegistry.Deregister Delete error", slog.Any("err", err))
		}
		cancel()
	}, nil
}

func (r Registry) registerWithRetry(ctx context.Context, key, val string) (clientv3.LeaseID, error) {
	ttl := grantTTL
	if r.config != nil && r.config.TTL > 0 {
		ttl = r.config.TTL
	}
	backoff := time.Millisecond * 200
	var leaseid clientv3.LeaseID
	for {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		// 申请租约
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Second*3)
		lease, err := r.client.Grant(timeoutCtx, ttl)
		cancel()
		if err != nil {
			logger.Error("etcdRegistry.Register Grant error", slog.Any("err", err))
			time.Sleep(backoff)
			if backoff < time.Second*3 {
				backoff *= 2
			}
			continue
		}
		leaseid = lease.ID
		// 写入带租约的键
		putCtx, cancel := context.WithTimeout(ctx, time.Second*3)
		_, err = r.client.Put(putCtx, key, val, clientv3.WithLease(leaseid))
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Error("etcdRegistry.Register Put error", slog.Any("err", err))
			}
			time.Sleep(backoff)
			if backoff < time.Second*3 {
				backoff *= 2
			}
			continue
		}
		return leaseid, nil
	}
}

func (r Registry) keepAliveLoop(ctx context.Context, key, val string, leaseid clientv3.LeaseID) {
outer:
	for {
		kaCtx, kaCancel := context.WithCancel(ctx)
		ch, err := r.client.KeepAlive(kaCtx, leaseid)
		if err != nil {
			kaCancel()
			logger.Error("etcdRegistry.KeepAlive start error", slog.Any("err", err))
			// 尝试重新注册
			newLease, regErr := r.registerWithRetry(ctx, key, val)
			if regErr != nil {
				logger.Error("etcdRegistry.re-register failed", slog.Any("err", regErr))
				return
			}
			leaseid = newLease
			continue outer
		}
	inner:
		for {
			select {
			case <-ctx.Done():
				kaCancel()
				func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
					defer cancel()
					if _, err := r.client.Revoke(ctx, leaseid); err != nil {
						logger.Error("etcdRegistry.Register Revoke error", slog.Any("err", err))
					}
				}()
				return
			case _, ok := <-ch:
				if !ok {
					kaCancel()
					// 断开，重新注册并重启 KeepAlive
					newLease, regErr := r.registerWithRetry(ctx, key, val)
					if regErr != nil {
						logger.Error("etcdRegistry.re-register failed", slog.Any("err", regErr))
						return
					}
					leaseid = newLease
					break inner
				}
			}
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
	return strings.TrimPrefix(strings.TrimPrefix(key, prefix), "/")
}

func isGrpcService(v discover.ServiceInfo) bool {
	if v.Kind == "grpc" {
		return true
	}
	if v.Metadata != nil && v.Metadata["kind"] == "grpc" {
		return true
	}
	return false
}

func buildSortedServices(endpoints map[string]discover.ServiceInfo) []discover.ServiceInfo {
	var services []discover.ServiceInfo
	for _, v := range endpoints {
		services = append(services, v)
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].Name == services[j].Name {
			return services[i].ID < services[j].ID
		}
		return services[i].Name < services[j].Name
	})
	return services
}

func (r Registry) applyPutEvent(endpoints map[string]discover.ServiceInfo, path string, key string, val []byte) []discover.ServiceInfo {
	v := discover.ServiceInfo{}
	v.Unmarshal(val)
	v.ID = getInstanceFromKey(key, path)
	if isGrpcService(v) {
		endpoints[v.ID] = v
	} else {
		delete(endpoints, v.ID)
	}
	return buildSortedServices(endpoints)
}

func (r Registry) applyDeleteEvent(endpoints map[string]discover.ServiceInfo, path string, key string) []discover.ServiceInfo {
	id := getInstanceFromKey(key, path)
	delete(endpoints, id)
	return buildSortedServices(endpoints)
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
		if !isGrpcService(v) {
			continue
		}
		services = append(services, v)
	}
	// 稳定排序
	sort.Slice(services, func(i, j int) bool {
		if services[i].Name == services[j].Name {
			return services[i].ID < services[j].ID
		}
		return services[i].Name < services[j].Name
	})
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
		if !isGrpcService(v) {
			continue
		}
		endpoints[v.ID] = v
	}
	update(buildSortedServices(endpoints))
	w := clientv3.NewWatcher(r.client)
	defer w.Close()
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
			for _, ev := range event.Events {
				key := string(ev.Kv.Key)
				switch ev.Type {
				case clientv3.EventTypePut:
					update(r.applyPutEvent(endpoints, path, key, ev.Kv.Value))
				case clientv3.EventTypeDelete:
					update(r.applyDeleteEvent(endpoints, path, key))
				}
			}
		}
	}
}
