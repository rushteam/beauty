package nacos

import (

	// "github.com/ymcvalu/grpc-discovery/pkg/instance"

	"strings"

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
	serviceName := target.Endpoint()
	registry := NewRegistry(&Config{
		Addr:      strings.Split(target.URL.Host, ","),
		Cluster:   "",
		Namespace: "",
		Group:     "",
	})
	r := grpcclient.NewResolver(cc, serviceName, registry)
	go r.Start()
	return r, nil
}
