package polaris

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

func (b *builder) Scheme() string {
	return "polaris"
}

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	reg, err := NewFromURL(target.URL)
	if err != nil {
		return nil, fmt.Errorf("polaris.resolver.Register: new config failed: %w", err)
	}
	r := grpcclient.NewResolver(cc, target.Endpoint(), reg)
	go r.Start()
	return r, nil
}
