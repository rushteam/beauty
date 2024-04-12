package nacos

import (

	// "github.com/ymcvalu/grpc-discovery/pkg/instance"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

func (b *builder) Scheme() string {
	return "nacos"
}

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r := grpcclient.NewResolver(cc, target.Endpoint(), BuildRegistryWithURL(target.URL))
	go r.Start()
	return r, nil
}
