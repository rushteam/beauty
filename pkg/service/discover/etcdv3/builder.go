package etcdv3

import (
	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

func (b *builder) Scheme() string {
	return "etcd"
}

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	serviceName := target.Endpoint()
	reg, err := NewFromURL(target.URL)
	if err != nil {
		return nil, err
	}
	r := grpcclient.NewResolver(cc, serviceName, reg)
	go r.Start()
	return r, nil
}
