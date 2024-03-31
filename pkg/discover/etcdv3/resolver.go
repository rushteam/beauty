package etcdv3

import (

	// "github.com/ymcvalu/grpc-discovery/pkg/instance"

	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&etcdBuilder{})
}

type etcdBuilder struct {
	// stop    chan struct{}
	// cc      resolver.ClientConn
	// key     string
	// backoff func(int) time.Duration
	// watcher *watcher
}

func (b *etcdBuilder) Scheme() string {
	return "etcd"
}

func (b *etcdBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	ns := strings.SplitN(target.Endpoint(), "/", 2)
	if len(ns) != 2 {
		return nil, fmt.Errorf("unexpected namespace or serviceName: %v", target.Endpoint())
	}
	namespace := ns[0]
	serviceName := ns[1]
	password, _ := target.URL.User.Password()

	updateState := func(services []discover.ServiceInfo) {
		if len(services) > 0 {
			cc.UpdateState(buildState(services))
		}
	}
	discovery := NewRegistry(&EtcdConfig{
		Endpoints: strings.Split(target.URL.Host, ","),
		Username:  target.URL.User.Username(),
		Password:  password,
		Namespace: namespace,
	})
	ctx, cancel := context.WithCancel(context.Background())
	r := &Resolver{stop: make(chan struct{})}
	go func() {
		<-r.stop
		defer cancel()
	}()
	go func() {
		if err := discovery.Watch(ctx, serviceName, updateState); err != nil {
			logger.Error("discovery watch failed", slog.Any("err", err))
		}
	}()
	return r, nil
}

func buildState(services []discover.ServiceInfo) resolver.State {
	addrs := make([]resolver.Address, 0, len(services))
	// endpoints := make([]resolver.Endpoint, 0, len(services))
	for _, v := range services {
		addrs = append(addrs, resolver.Address{
			Addr:       v.Addr,
			ServerName: v.Name,
		})
		// endpoints = append(endpoints, resolver.Endpoint{
		// 	Addresses: []resolver.Address{
		// 		{
		// 			Addr:       v.Addr,
		// 			ServerName: v.Name,
		// 		},
		// 	},
		// 	// Attributes: &attributes.Attributes{},
		// })
	}
	// fmt.Println("Updating service endpoints", endpoints)
	return resolver.State{
		Addresses: addrs,
		// Endpoints: endpoints,
		// ServiceConfig: &serviceconfig.ParseResult{},
		// Attributes:    &attributes.Attributes{},
	}
}

type Resolver struct {
	stop chan struct{}
}

func (r *Resolver) ResolveNow(opt resolver.ResolveNowOptions) {
	// fmt.Println("ResolveNow", opt)
}

func (r *Resolver) Close() {
	close(r.stop)
}
