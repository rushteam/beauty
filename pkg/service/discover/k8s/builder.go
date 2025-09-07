package k8s

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"google.golang.org/grpc/resolver"
)

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

// Scheme 返回解析器的方案名称
func (b *builder) Scheme() string {
	return "k8s"
}

// Build 构建解析器
func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	config, err := NewFromURL(target.URL)
	if err != nil {
		return nil, fmt.Errorf("k8s.resolver.Build: new config failed: %w", err)
	}

	reg := NewRegistry(config)
	if reg == nil {
		return nil, fmt.Errorf("k8s.resolver.Build: failed to create registry")
	}

	serviceName := target.Endpoint()
	r := grpcclient.NewResolver(cc, serviceName, reg)
	go r.Start()
	return r, nil
}
