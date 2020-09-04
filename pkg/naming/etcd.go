package naming

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/resolver"
)

//EtcdScheme ..
const EtcdScheme = "etcd"

func init() {
	resolver.Register(&etcdBuilder{scheme: EtcdScheme, prefix: ""})
}

type etcdBuilder struct {
	scheme    string
	prefix    string
	endpoints []string
}

//Build ..
func (b *etcdBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	if err := b.parseTarget(target); err != nil {
		return nil, err
	}
	reg, err := New(b.endpoints)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &etcdResolver{
		cc:     cc,
		ctx:    ctx,
		cancel: cancel,
	}
	watch, err := reg.Subscriber(ctx, b.prefix)
	if err != nil {
		return nil, err
	}
	r.watch = watch
	go func() {
		select {
		case nodes, ok := <-r.watch:
			if !ok {
				return
			}
			var addrs []resolver.Address
			for _, v := range nodes {
				var node Node
				if err := node.Unmarshal(v); err != nil {
					continue
				}
				addrs = append(addrs, resolver.Address{
					ServerName: node.ServerName,
					Addr:       node.Addr,
					Attributes: node.Attributes,
				})
			}
			r.cc.UpdateState(resolver.State{
				Addresses: addrs,
			})
		}
	}()
	return r, nil
}

//Scheme ..
func (b *etcdBuilder) Scheme() string {
	return b.scheme
}

//parseTarget ..
func (b *etcdBuilder) parseTarget(target resolver.Target) error {
	if target.Endpoint == "" {
		return fmt.Errorf("grpc: naming: etcd config error, scheme://auth:/addr1[,addr2]/key")
	}
	seg := strings.SplitN(target.Endpoint, "/", 2)
	if len(seg) != 2 {
		return fmt.Errorf("grpc: naming: etcd config error, scheme://auth:/addr1[,addr2]/key")
	}
	b.endpoints = strings.Split(seg[0], ",")
	b.prefix = seg[1]
	return nil
}

type etcdResolver struct {
	cc     resolver.ClientConn
	ctx    context.Context
	cancel func()
	watch  <-chan map[string][]byte
}

func (r *etcdResolver) Close() {
	r.cancel()
}

func (r *etcdResolver) ResolveNow(opt resolver.ResolveNowOptions) {}
