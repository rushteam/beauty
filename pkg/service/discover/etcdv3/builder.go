package etcdv3

import (
	"strings"

	"github.com/rushteam/beauty/pkg/client/grpc"
	"google.golang.org/grpc/resolver"
)

// "github.com/ymcvalu/grpc-discovery/pkg/instance"

func init() {
	resolver.Register(&builder{})
}

type builder struct{}

func (b *builder) Scheme() string {
	return "etcd"
}

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	serviceName := target.Endpoint()
	password, _ := target.URL.User.Password()
	registry := NewRegistry(&Config{
		Endpoints: strings.Split(target.URL.Host, ","),
		Username:  target.URL.User.Username(),
		Password:  password,
		Prefix:    "beauty",
	})
	r := grpcclient.NewResolver(cc, serviceName, registry)
	go r.Start()
	return r, nil
}
