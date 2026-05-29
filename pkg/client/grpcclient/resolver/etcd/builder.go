// Package etcd registers an etcd-backed gRPC name resolver.
// Import this package with a blank identifier to activate the resolver:
//
//	import _ "github.com/rushteam/beauty/pkg/client/grpcclient/resolver/etcd"
package etcd

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	etcdv3 "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{scheme: "etcd"})
	resolver.Register(&builder{scheme: "etcdv3"})
}

type builder struct{ scheme string }

func (b *builder) Scheme() string { return b.scheme }

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	reg, err := etcdv3.NewFromURL(target.URL)
	if err != nil {
		return nil, fmt.Errorf("etcd resolver: %w", err)
	}
	r := grpcclient.NewResolver(cc, target.Endpoint(), reg)
	go r.Start()
	return r, nil
}
