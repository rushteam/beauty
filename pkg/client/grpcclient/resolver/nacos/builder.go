// Package nacos registers a Nacos-backed gRPC name resolver.
// Import this package with a blank identifier to activate the resolver:
//
//	import _ "github.com/rushteam/beauty/pkg/client/grpcclient/resolver/nacos"
package nacos

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	nacosdisc "github.com/rushteam/beauty/pkg/service/discover/nacos"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

func (b *builder) Scheme() string { return "nacos" }

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	reg, err := nacosdisc.NewFromURL(target.URL)
	if err != nil {
		return nil, fmt.Errorf("nacos resolver: %w", err)
	}
	r := grpcclient.NewResolver(cc, target.Endpoint(), reg)
	go r.Start()
	return r, nil
}
