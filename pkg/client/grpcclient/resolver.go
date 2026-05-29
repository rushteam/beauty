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
		slog.Info("grpclient service update", slog.Int("count", len(services)), slog.Any("service", services))
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

func buildState(services []discover.ServiceInfo) resolver.State {
	addrs := make([]resolver.Address, 0, len(services))
	for _, s := range services {
		var attr *grpcattr.Attributes
		for k, v := range s.Metadata {
			attr = attr.WithValue(k, v)
		}
		addrs = append(addrs, resolver.Address{
			Addr:       s.Addr,
			Attributes: attr,
		})
	}
	return resolver.State{Addresses: addrs}
}
