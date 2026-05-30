// Package consul registers a Consul-backed gRPC name resolver.
// Import this package with a blank identifier to activate the resolver:
//
//	import _ "github.com/rushteam/beauty/pkg/client/grpcclient/resolver/consul"
package consul

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	consuldisc "github.com/rushteam/beauty/pkg/service/discover/consul"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

func (b *builder) Scheme() string { return "consul" }

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	reg, err := consuldisc.NewFromURL(target.URL)
	if err != nil {
		return nil, fmt.Errorf("consul resolver: %w", err)
	}
	r := grpcclient.NewResolver(cc, target.Endpoint(), reg)
	go r.Start()
	return r, nil
}
