// Package polaris registers a Polaris-backed gRPC name resolver.
// Import this package with a blank identifier to activate the resolver:
//
//	import _ "github.com/rushteam/beauty/pkg/client/grpcclient/resolver/polaris"
package polaris

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	polarisdisc "github.com/rushteam/beauty/pkg/service/discover/polaris"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

func (b *builder) Scheme() string { return "polaris" }

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	reg, err := polarisdisc.NewFromURL(target.URL)
	if err != nil {
		return nil, fmt.Errorf("polaris resolver: %w", err)
	}
	wrapped := grpcclient.WrapWithFilter(cc, target.URL.Query())
	r := grpcclient.NewResolver(wrapped, target.Endpoint(), reg)
	go r.Start()
	return r, nil
}
