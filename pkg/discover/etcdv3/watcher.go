package etcdv3

import (
	"context"

	"github.com/rushteam/beauty/pkg/discover"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type watcher struct {
	ctx     context.Context
	cancel  context.CancelFunc
	client  *clientv3.Client
	watcher clientv3.Watcher
	kv      clientv3.KV
}

func newWatcher(ctx context.Context) *watcher {
	ctx, cancel := context.WithCancel(ctx)
	client, _ := clientv3.New(clientv3.Config{})
	return &watcher{
		ctx:     ctx,
		cancel:  cancel,
		client:  client,
		watcher: clientv3.NewWatcher(client),
		kv:      clientv3.NewKV(client),
	}

}

func (w *watcher) Next(ctx context.Context) ([]*discover.Service, error) {
	return []*discover.Service{}, nil
}

func (w *watcher) Close() error {
	w.cancel()
	return nil
}
