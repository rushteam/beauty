package etcdv3

import (
	"context"
	"fmt"
	"log"

	"github.com/rushteam/beauty/pkg/discover"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type watcher struct {
	ctx       context.Context
	cancal    context.CancelFunc
	ch        clientv3.WatchChan
	endpoints map[string]*discover.ServiceInfo
}

func newWatcher(c *EtcdConfig, name string) *watcher {
	client, _ := clientv3.New(clientv3.Config{
		Endpoints: c.Endpoints,
		Username:  c.Username,
		Password:  c.Password,
	})
	ctx, cancal := context.WithCancel(context.Background())
	key := fmt.Sprintf("%s/%s", c.Namespace, name)
	return &watcher{
		ctx:    ctx,
		cancal: cancal,
		//clientv3.WithRev(rev+1)
		ch:        client.Watch(ctx, key, clientv3.WithPrefix(), clientv3.WithProgressNotify()),
		endpoints: make(map[string]*discover.ServiceInfo),
	}

}

func (w *watcher) Next() (map[string]*discover.ServiceInfo, error) {
	select {
	case <-w.ctx.Done():
		return w.endpoints, w.ctx.Err()
	case event := <-w.ch:
		if event.Canceled {
			log.Printf("failed to watch server addresses changed, caused by: %v", event.Err())
			return w.endpoints, w.ctx.Err()
		}
		for _, ev := range event.Events {
			key := string(ev.Kv.Key)
			// ev.IsCreate()
			switch ev.Type {
			case clientv3.EventTypePut:
				v := discover.ServiceInfo{}
				v.Unmarshal(ev.Kv.Value)
				w.endpoints[key] = &v
			case clientv3.EventTypeDelete:
				delete(w.endpoints, key)
			}
		}
	}
	return w.endpoints, nil
}
