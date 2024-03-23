package etcdv3

import (
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
		r.cc.UpdateState(resolver.State{
			Addresses: getAddrs(endpoints),
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

func getAddrs(endpoints map[string]*discover.ServiceInfo) []resolver.Address {
	addrs := make([]resolver.Address, 0, len(endpoints))
	for _, v := range endpoints {
		if v == nil {
			continue
		}
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
