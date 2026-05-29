// Package k8s registers a Kubernetes-backed gRPC name resolver.
// Import this package with a blank identifier to activate the resolver:
//
//	import _ "github.com/rushteam/beauty/pkg/client/grpcclient/resolver/k8s"
package k8s

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	k8sdisc "github.com/rushteam/beauty/pkg/service/discover/k8s"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{scheme: "k8s"})
	resolver.Register(&builder{scheme: "kubernetes"})
}

type builder struct{ scheme string }

func (b *builder) Scheme() string { return b.scheme }

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	config, err := k8sdisc.NewFromURL(target.URL)
	if err != nil {
		return nil, fmt.Errorf("k8s resolver: %w", err)
	}
	reg := k8sdisc.NewRegistry(config)
	if reg == nil {
		return nil, fmt.Errorf("k8s resolver: failed to create registry")
	}
	r := grpcclient.NewResolver(cc, target.Endpoint(), reg)
	go r.Start()
	return r, nil
}
