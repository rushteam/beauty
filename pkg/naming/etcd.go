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
	resolver.Register(&etcdBuilder{})
}

type etcdBuilder struct {
	Prefix string
}
type etcdTarget struct {
	Scheme    string
	Authority string
	Endpoints []string
	Prefix    string
}

//parseTarget ..
func parseTarget(target resolver.Target) (*etcdTarget, error) {
	t := &etcdTarget{
		Scheme:    target.Scheme,
		Authority: target.Authority,
	}
	if target.Endpoint == "" {
		return t, fmt.Errorf("grpc: naming: etcd config error, scheme://auth:/addr1[,addr2]/key")
	}
	seg := strings.SplitN(target.Endpoint, "/", 2)
	if len(seg) != 2 {
		return t, fmt.Errorf("grpc: naming: etcd config error, scheme://auth:/addr1[,addr2]/key")
	}
	t.Endpoints = strings.Split(seg[0], ",")
	t.Prefix = seg[1]
	return t, nil
}

//Build ..
func (b *etcdBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	tar, err := parseTarget(target)
	if err != nil {
		return nil, err
	}
	reg, err := New(tar.Endpoints)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &etcdResolver{
		cc:     cc,
		ctx:    ctx,
		cancel: cancel,
	}
	watch, err := reg.Subscriber(ctx, prefix)
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
	return EtcdScheme
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
