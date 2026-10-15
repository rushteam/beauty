package grpcclient

import (
	"context"
	"log/slog"
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

// metadataAttrKey 是 Attributes 中存储完整 metadata 的 key 类型。
type metadataAttrKey struct{}

// metadataAttr 包装 map[string]string，实现 Equal(any) bool，
// 避免 gRPC ≥1.60 attributes.Equal 对不可比较类型回落到 != 时 panic。
// 参见 google.golang.org/grpc/attributes.(*Attributes).Equal。
type metadataAttr map[string]string

// Equal 满足 gRPC attributes 的 equaler 接口。
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
		var attr *grpcattr.Attributes
		if len(s.Metadata) > 0 {
			// 必须包装为 metadataAttr：裸 map 不可比较，
			// pickfirst 在地址更新时调用 Attributes.Equal 会 panic。
			attr = attr.WithValue(metadataAttrKey{}, metadataAttr(s.Metadata))
		}
		addrs = append(addrs, resolver.Address{
			Addr:       s.Addr,
			Attributes: attr,
		})
	}
	return resolver.State{Addresses: addrs}
}
