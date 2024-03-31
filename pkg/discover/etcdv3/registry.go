package etcdv3

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var grantTTL int64 = 10

var m = make(map[string]*EtcdRegistry)

func NewRegistry(c *Config) *EtcdRegistry {
	key := c.String()
	if v, ok := m[key]; ok {
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
	r := &EtcdRegistry{
		client:    client,
		namespace: c.Namespace,
		config:    c,
	}
	m[key] = r
	return r
}

type EtcdRegistry struct {
	config    *Config
	client    *clientv3.Client
	namespace string
	discover.Registry
	discover.Discovery
}

func (r EtcdRegistry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	value := discover.ServiceInfo{
		Name:     info.Name(),
		Addr:     info.Addr(),
		Metadata: info.Metadata(),
	}
	key := buildServiceKey(r.namespace, info.Name(), info.ID())
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

func (r EtcdRegistry) keepAlive(ctx context.Context, key, val string) {
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

func buildServiceKey(ns, name, id string) string {
	return fmt.Sprintf("%s/%s/%s", ns, name, id)
}
