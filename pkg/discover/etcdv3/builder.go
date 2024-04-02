package etcdv3

import (
	"strings"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"google.golang.org/grpc/resolver"
)

// "github.com/ymcvalu/grpc-discovery/pkg/instance"

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

func (b *builder) Scheme() string {
	return "etcd"
}

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	serviceName := target.Endpoint()
	password, _ := target.URL.User.Password()
	registry := NewRegistry(&Config{
		Endpoints: strings.Split(target.URL.Host, ","),
		Username:  target.URL.User.Username(),
		Password:  password,
		Prefix:    "beauty",
	})
	r := grpcclient.NewResolver(cc, serviceName, registry)
	go r.Start()
	return r, nil
}

/*

func buildState(services []discover.ServiceInfo) resolver.State {
	addrs := make([]resolver.Address, 0, len(services))
	attributes := &attributes.Attributes{}
	// endpoints := make([]resolver.Endpoint, 0, len(services))
	for _, s := range services {
		addrs = append(addrs, resolver.Address{
			Addr:       s.Addr,
			ServerName: s.Name,
		})
		for k, v := range s.Metadata {
			attributes.WithValue(k, v)
		}
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
		//Endpoints 不知道为啥用不了,文档中说Addresses要废弃换Endpoints 没仔细研究 基本上都是用的Addresses
		// Endpoints: endpoints,
		// ServiceConfig: &serviceconfig.ParseResult{},
		Attributes: attributes,
	}
}

type Resolver struct {
	cc          resolver.ClientConn
	ctx         context.Context
	cancel      context.CancelFunc
	serviceName string
	discovery   discover.Discovery
}

func (r *Resolver) ResolveNow(opt resolver.ResolveNowOptions) {
	// fmt.Println("ResolveNow", opt)
}

func (r *Resolver) Close() {
	r.cancel()
}

func (r *Resolver) start() {
	updateState := func(services []discover.ServiceInfo) {
		if len(services) > 0 {
			r.cc.UpdateState(buildState(services))
		}
	}
	if err := r.discovery.Watch(r.ctx, r.serviceName, updateState); err != nil {
		logger.Error("discovery watch failed", slog.Any("err", err))
	}
}
*/
