package etcdv3

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type etcdRegistry struct {
	client    *clientv3.Client
	namespace string
}

func (r etcdRegistry) Register(ctx context.Context, info discover.Service) error {
	value := discover.ServiceInfo{
		Name:     info.Name(),
		Addr:     info.Addr(),
		Metadata: info.Metadata(),
	}
	key := buildServiceKey(r.namespace, info.Name(), info.ID())
	go r.keepAlive(ctx, key, value.Marshal())
	return nil
}

func (r etcdRegistry) Deregister(ctx context.Context, info discover.Service) error {
	key := buildServiceKey(r.namespace, info.Name(), info.ID())
	_, err := r.client.Delete(ctx, key)
	return err
}

func (r etcdRegistry) keepAlive(ctx context.Context, key, val string) {
	ttl := int64(10)
	t := time.NewTicker(time.Second * time.Duration(ttl/2))
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			lease, err := r.client.Grant(ctx, ttl)
			if err != nil {
				logger.Error("etcdRegistry.Register Grant error: %v", err)
				return
			}
			_, err = r.client.Put(ctx, key, val, clientv3.WithLease(lease.ID))
			if err != nil {
				logger.Error("etcdRegistry.Register Put error: %v", err)
			}
		}
	}
}

func NewEtcdRegistry(c EtcdConfig) discover.Registry {
	client, err := clientv3.New(clientv3.Config{
		Endpoints: c.Endpoints,
		Username:  c.Username,
		Password:  c.Password,
	})
	if err != nil {
		logger.Error("etcdRegistry client error", slog.Any("err", err))
		// return discover.NewNoop()
	}
	return &etcdRegistry{
		client:    client,
		namespace: c.Namespace,
	}
}

func buildServiceKey(ns, name, id string) string {
	return fmt.Sprintf("%s/%s/%s", ns, name, id)
}
