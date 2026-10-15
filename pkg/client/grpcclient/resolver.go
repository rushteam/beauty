package grpcclient

import (
	"context"
	"log/slog"
	"maps"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	grpcattr "google.golang.org/grpc/attributes"
	"google.golang.org/grpc/resolver"
)

type Resolver struct {
	cc          resolver.ClientConn
	ctx         context.Context
	cancel      context.CancelFunc
	serviceName string
	discovery   discover.Discovery
}

func NewResolver(cc resolver.ClientConn, serviceName string, discovery discover.Discovery) *Resolver {
	ctx, cancel := context.WithCancel(context.Background())
	return &Resolver{
		cc:          cc,
		ctx:         ctx,
		cancel:      cancel,
		serviceName: serviceName,
		discovery:   discovery,
	}
}

func (r *Resolver) ResolveNow(opt resolver.ResolveNowOptions) {
	// fmt.Println("ResolveNow", opt)
}

func (r *Resolver) Close() {
	r.cancel()
}

func (r *Resolver) Start() {
	updateState := func(services []discover.ServiceInfo) {
		slog.Debug("grpclient service update", slog.Int("count", len(services)), slog.Any("service", services))
		if err := r.cc.UpdateState(buildState(services)); err != nil {
			slog.Error("discovery updateState failed", slog.Any("err", err))
		}
	}
	backoff := 200 * time.Millisecond
	for {
		if r.ctx.Err() != nil {
			return
		}
		if err := r.discovery.Watch(r.ctx, r.serviceName, updateState); err != nil {
			slog.Error("discovery watch failed", slog.Any("err", err))
		}
		// Watch 返回 nil 表示 ctx 已取消（正常退出）或 backend 断连
		if r.ctx.Err() != nil {
			return
		}
		slog.Warn("resolver watch exited unexpectedly, reconnecting",
			slog.String("service", r.serviceName),
			slog.Duration("backoff", backoff))
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(backoff):
			if backoff < 8*time.Second {
				backoff *= 2
			}
		}
	}
}

// metadataAttrKey 是 BalancerAttributes 中存储服务发现 metadata 的 key 类型。
type metadataAttrKey struct{}

// metadataAttr 包装 map[string]string，实现 Equal(any) bool。
// 服务发现 metadata（weight/region/label 等）属于 LB 侧信息，按 gRPC 约定放在
// BalancerAttributes（不影响 SubConn 建连；pickfirst 比较地址时会忽略该字段）。
// Equal 仍保留：自定义 LB / 未来 Endpoint 路径若比较 BalancerAttributes，
// 裸 map 回落 != 仍会 panic。参见 google.golang.org/grpc/attributes。
type metadataAttr map[string]string

// Equal 满足 gRPC attributes 的 equaler 接口，按 key/value 深比较。
func (m metadataAttr) Equal(o any) bool {
	other, ok := o.(metadataAttr)
	if !ok {
		return false
	}
	if len(m) != len(other) {
		return false
	}
	for k, v := range m {
		if ov, ok := other[k]; !ok || ov != v {
			return false
		}
	}
	return true
}

func buildState(services []discover.ServiceInfo) resolver.State {
	addrs := make([]resolver.Address, 0, len(services))
	for _, s := range services {
		var balAttr *grpcattr.Attributes
		if len(s.Metadata) > 0 {
			// Clone：Attributes 值写入后不可再改；共享上游 map 会被悄然污染。
			balAttr = balAttr.WithValue(metadataAttrKey{}, metadataAttr(maps.Clone(s.Metadata)))
		}
		addrs = append(addrs, resolver.Address{
			Addr:               s.Addr,
			BalancerAttributes: balAttr,
		})
	}
	return resolver.State{Addresses: addrs}
}
