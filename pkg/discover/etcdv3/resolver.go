package etcdv3

import (
	"fmt"
	"strings"
	"time"

	// "github.com/ymcvalu/grpc-discovery/pkg/instance"

	"github.com/rushteam/beauty/pkg/discover"
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
	cc      resolver.ClientConn
	key     string
	backoff func(int) time.Duration
	watcher *watcher
}

func (b *etcdResolver) Scheme() string {
	return "etcd"
}

func (b *etcdResolver) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r := &etcdResolver{
		cc:      cc,
		key:     strings.TrimRight(target.Endpoint(), "/"),
		stop:    make(chan struct{}),
		backoff: backoff,
		watcher: newWatcher(&EtcdConfig{
			Endpoints: strings.Split(target.URL.Host, ","),
		}, target.Endpoint()),
	}
	go r.watch()
	return r, nil
}

func (r *etcdResolver) watch() {
	for {
		endpoints, err := r.watcher.Next()
		if err != nil {
			continue
		}
		addrs := getAddrs(endpoints)
		fmt.Println(addrs)
		r.cc.UpdateState(resolver.State{
			Addresses: addrs,
		})
	}
}

func (r *etcdResolver) ResolveNow(opt resolver.ResolveNowOptions) {
	// fmt.Println("ResolveNow", opt)
}

func (r *etcdResolver) Close() {
	r.stop <- struct{}{}
	r.watcher.ctx.Done()
}

// func (r *etcdResolver) watch(ctx context.Context) {
// if r.stopped() {
// 	return
// }
// var (
// 	endpoints  = make(map[string]*discover.ServiceInfo)
// 	rev        int64
// 	retryTimes int
// )

// for {
// 	ctx, cancel := context.WithTimeout(ctx, time.Second*3)
// 	resp, err := r.client.Get(ctx, r.key, clientv3.WithPrefix())
// 	cancel()
// 	fmt.Println("watch >", r.key, resp, err)
// 	if err != nil {
// 		log.Printf("[error]failed to resolve addr, caused by %s", err)
// 		delay := r.backoff(retryTimes)
// 		retryTimes++
// 		time.Sleep(delay)
// 		continue
// 	}

// 	retryTimes = 0
// 	rev = resp.Header.Revision

// 	for _, kv := range resp.Kvs {
// 		v := discover.ServiceInfo{}
// 		if err := v.Unmarshal(kv.Value); err == nil {
// 			endpoints[string(kv.Key)] = &v
// 		}
// 	}
// 	addrs := r.getAddrs(endpoints)
// 	r.cc.UpdateState(resolver.State{
// 		Addresses: addrs,
// 	})
// 	break
// }
// }

func getAddrs(endpoints map[string]*discover.ServiceInfo) []resolver.Address {
	addrs := make([]resolver.Address, 0, len(endpoints))
	for _, v := range endpoints {
		if v == nil {
			continue
		}
		// addr.Metadata = &v.Metadata // the addr.Metadata will be hashed, so we should use pointer
		addrs = append(addrs, resolver.Address{
			Addr:       v.Addr,
			ServerName: v.Name,
		})
	}
	return addrs
}

func backoff(i int) time.Duration {
	if i > 5 {
		i = 5
	}
	return time.Duration(i) * time.Second
}
