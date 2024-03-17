package etcdv3

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	// "github.com/ymcvalu/grpc-discovery/pkg/instance"

	"github.com/rushteam/beauty/pkg/discover"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(newEtcdResolver())
}

func newEtcdResolver() resolver.Builder {
	return &etcdResolver{}
}

type etcdResolver struct {
	stop    chan struct{}
	cancel  context.CancelFunc
	cc      resolver.ClientConn
	client  *clientv3.Client
	key     string
	backoff func(int) time.Duration
}

func (b *etcdResolver) Scheme() string {
	return "etcd"
}

func (b *etcdResolver) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &etcdResolver{
		cc:  cc,
		key: strings.TrimRight(target.Endpoint(), "/"),
		// client: client,
		stop:   make(chan struct{}),
		cancel: cancel,
		backoff: func(i int) time.Duration {
			if i > 5 {
				i = 5
			}
			return time.Duration(i) * time.Second
		},
	}
	endpoints := strings.Split(target.URL.Host, ",")
	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
		// Endpoints: cfg.Endpoints,
		// Username:    cfg.Username,
		// Password:    cfg.Password,
		// DialTimeout: cfg.Timeout,
	})
	if err != nil {
		return nil, nil
	}
	r.client = client
	go func() {
		for {
			time.Sleep(time.Second * 2)
			client.Put(ctx, r.key, "{}")
		}
	}()
	go r.watch(ctx)
	fmt.Println("...")
	return r, nil
}

func (r *etcdResolver) ResolveNow(resolver.ResolveNowOptions) {
}

func (r *etcdResolver) Close() {
	r.stop <- struct{}{}
	r.cancel()
	// r.doneOnce.Do(func() {
	// 	close(r.done)
	// 	r.client.Close()
	// })
}

func (r *etcdResolver) watch(ctx context.Context) {
	if r.stopped() {
		return
	}
	var (
		endpoints  = make(map[string]*discover.ServiceInfo)
		rev        int64
		retryTimes int
	)

	for {
		ctx, cancel := context.WithTimeout(ctx, time.Second*3)
		resp, err := r.client.Get(ctx, r.key, clientv3.WithPrefix())
		cancel()
		fmt.Println("watch >", r.key, resp, err)
		if err != nil {
			log.Printf("[error]failed to resolve addr, caused by %s", err)
			delay := r.backoff(retryTimes)
			retryTimes++
			time.Sleep(delay)
			continue
		}

		retryTimes = 0
		rev = resp.Header.Revision

		for _, kv := range resp.Kvs {
			v := discover.ServiceInfo{}
			if err := v.Unmarshal(kv.Value); err == nil {
				endpoints[string(kv.Key)] = &v
			}
		}
		addrs := r.getAddrs(endpoints)
		r.cc.UpdateState(resolver.State{
			Addresses: addrs,
		})
		break
	}
	if r.stopped() {
		return
	}
	ch := r.client.Watch(ctx, r.key, clientv3.WithPrefix(), clientv3.WithProgressNotify(), clientv3.WithRev(rev+1))
	for {
		select {
		case <-r.stop:
			r.cancel()
			return

		case event := <-ch:
			if event.Canceled {
				log.Printf("failed to watch server addresses changed, caused by: %v", event.Err())
				// r.cancel()

				// delay := r.backoff(retryTimes)
				// retryTimes++
				// time.Sleep(delay)
				// watchCh = r.client.Watch(ctx, r.key, clientv3.WithPrefix(), clientv3.WithProgressNotify(), clientv3.WithRev(rev+1))
				continue
			}
			fmt.Println("...", event.Events)
			for _, ev := range event.Events {
				key := string(ev.Kv.Key)
				// ev.IsCreate()
				switch ev.Type {
				case clientv3.EventTypePut:
					v := discover.ServiceInfo{}
					v.Unmarshal(ev.Kv.Value)
					endpoints[key] = &v
				case clientv3.EventTypeDelete:
					delete(endpoints, key)
				}
			}
		}
		retryTimes = 0
		addrs := r.getAddrs(endpoints)
		r.cc.UpdateState(resolver.State{
			Addresses: addrs,
		})
	}
}

func (r *etcdResolver) getAddrs(endpoints map[string]*discover.ServiceInfo) []resolver.Address {
	addrs := make([]resolver.Address, 0, len(endpoints))
	for _, v := range endpoints {
		if v == nil {
			continue
		}
		addr := resolver.Address{
			Addr:       v.Addr,
			ServerName: v.Name,
		}
		// addr.Metadata = &v.Metadata // the addr.Metadata will be hashed, so we should use pointer
		addrs = append(addrs, addr)
	}
	return addrs
}

func (r *etcdResolver) stopped() bool {
	select {
	case <-r.stop:
		return true
	default:
	}
	return false
}
