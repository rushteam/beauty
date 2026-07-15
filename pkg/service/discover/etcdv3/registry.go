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

	beautyetcd "github.com/rushteam/beauty/pkg/infra/etcd"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var grantTTL int64 = 10

var instance = make(map[string]*Registry)
var mu sync.Mutex

var _ discover.RegistryDiscovery = (*Registry)(nil)


func NewRegistry(c *Config) *Registry {
	key := c.String()
	mu.Lock()
	defer mu.Unlock()
	if v, ok := instance[key]; ok {
		return v
	}
	// 复用 pkg/infra/etcd 的连接构造,与配置中心/分布式锁共用同一处参数处理。
	client, err := beautyetcd.NewClient(&beautyetcd.Config{
		Endpoints: c.Endpoints,
		Username:  c.Username,
		Password:  c.Password,
		DialMS:    c.DialMS,
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

func (r *Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	if info.Kind() != "grpc" {
		return func() {}, fmt.Errorf("etcdRegistry only supports grpc services, got kind=%q", info.Kind())
	}
	value := discover.ServiceInfo{
		ID:       info.ID(),
		Kind:     info.Kind(),
		Name:     info.Name(),
		Addr:     info.Addr(),
		Metadata: info.Metadata(),
	}
	key := buildServiceKey(r.prefix, info.Name(), info.ID())

	valueStr, err := value.Marshal()
	if err != nil {
		return func() {}, fmt.Errorf("failed to marshal service info: %w", err)
	}

	// 同步首次注册（带重试）
	leaseID, err := r.registerWithRetry(ctx, key, valueStr)
	if err != nil {
		return func() {}, err
	}

	regCtx, stop := context.WithCancel(ctx)
	go r.keepAliveLoop(regCtx, key, valueStr, leaseID)
	return func() {
		stop()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()
		if _, err := r.client.Delete(ctx, key); err != nil {
			logger.Error("etcdRegistry.Deregister Delete error", slog.Any("err", err))
		}
	}, nil
}

func (r *Registry) registerWithRetry(ctx context.Context, key, val string) (clientv3.LeaseID, error) {
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

func (r *Registry) keepAliveLoop(ctx context.Context, key, val string, leaseid clientv3.LeaseID) {
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
	return fmt.Sprintf("/%s/%s/%s", strings.TrimPrefix(prefix, "/"), name, id)
}

func buildServicePath(prefix, name string) string {
	return fmt.Sprintf("/%s/%s", strings.TrimPrefix(prefix, "/"), name)
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

func (r *Registry) applyPutEvent(endpoints map[string]discover.ServiceInfo, path string, key string, val []byte) []discover.ServiceInfo {
	v := discover.ServiceInfo{}
	if err := v.Unmarshal(val); err != nil {
		logger.Error("etcdRegistry.applyPutEvent unmarshal error", slog.String("key", key), slog.Any("err", err))
		return buildSortedServices(endpoints)
	}
	v.ID = getInstanceFromKey(key, path)
	if isGrpcService(v) {
		endpoints[v.ID] = v
	} else {
		delete(endpoints, v.ID)
	}
	return buildSortedServices(endpoints)
}

func (r *Registry) applyDeleteEvent(endpoints map[string]discover.ServiceInfo, path string, key string) []discover.ServiceInfo {
	id := getInstanceFromKey(key, path)
	delete(endpoints, id)
	return buildSortedServices(endpoints)
}

func (r *Registry) Find(ctx context.Context, name string) ([]discover.ServiceInfo, error) {
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
		if err := v.Unmarshal(kv.Value); err != nil {
			logger.Error("etcdRegistry.Find unmarshal error", slog.String("key", string(kv.Key)), slog.Any("err", err))
			continue
		}
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

func (r *Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	path := buildServicePath(r.prefix, serviceName)
	backoff := 200 * time.Millisecond

	// 用带缓冲 channel 将 notify 回调与事件循环解耦，避免慢回调阻塞 watcher。
	// 缓冲为 1：只保留最新快照，旧的未消费时直接丢弃（调用方会收到最新状态）。
	notifyCh := make(chan []discover.ServiceInfo, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case services, ok := <-notifyCh:
				if !ok {
					return
				}
				update(services)
			}
		}
	}()
	notify := func(services []discover.ServiceInfo) {
		select {
		case notifyCh <- services:
		default:
			// 旧快照尚未消费，替换为最新
			select {
			case <-notifyCh:
			default:
			}
			notifyCh <- services
		}
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		endpoints, rev, err := r.watchSnapshot(ctx, path, notify)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Error("etcdRegistry.Watch snapshot error, retrying", slog.Any("err", err))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
				if backoff < 8*time.Second {
					backoff *= 2
				}
			}
			continue
		}
		backoff = 200 * time.Millisecond // 快照成功，重置退避

		disconnected := r.watchLoop(ctx, path, rev, endpoints, notify)
		if !disconnected {
			// ctx 已取消，正常退出
			return nil
		}
		// 断连，退避后重建 watcher
		logger.Warn("etcdRegistry.Watch disconnected, reconnecting", slog.String("service", serviceName))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
			if backoff < 8*time.Second {
				backoff *= 2
			}
		}
	}
}

// watchSnapshot 拉取初始快照并通知，返回当前 endpoints 和 revision。
func (r *Registry) watchSnapshot(ctx context.Context, path string, update discover.Notify) (map[string]discover.ServiceInfo, int64, error) {
	resp, err := r.client.Get(ctx, path, clientv3.WithPrefix())
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get service: %w", err)
	}
	endpoints := make(map[string]discover.ServiceInfo)
	for _, kv := range resp.Kvs {
		v := discover.ServiceInfo{}
		if err := v.Unmarshal(kv.Value); err != nil {
			logger.Error("etcdRegistry.Watch unmarshal error", slog.String("key", string(kv.Key)), slog.Any("err", err))
			continue
		}
		v.ID = getInstanceFromKey(string(kv.Key), path)
		if !isGrpcService(v) {
			continue
		}
		endpoints[v.ID] = v
	}
	update(buildSortedServices(endpoints))
	return endpoints, resp.Header.Revision, nil
}

// watchLoop 持续监听事件，直到 ctx 取消（返回 false）或 watcher 断连（返回 true）。
func (r *Registry) watchLoop(ctx context.Context, path string, rev int64, endpoints map[string]discover.ServiceInfo, update discover.Notify) (disconnected bool) {
	w := clientv3.NewWatcher(r.client)
	defer w.Close()
	ch := w.Watch(ctx, path,
		clientv3.WithPrefix(),
		clientv3.WithRev(rev),
	)
	if err := w.RequestProgress(ctx); err != nil {
		logger.Error("etcdRegistry.Watch RequestProgress error", slog.Any("err", err))
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case event, ok := <-ch:
			if !ok {
				logger.Warn("etcdRegistry.Watch channel closed, will reconnect")
				return true
			}
			if event.Canceled {
				logger.Warn("etcdRegistry.Watch event canceled, will reconnect", slog.Any("err", event.Err()))
				return true
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
