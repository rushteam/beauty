package grpcclient

import (

	// "github.com/ymcvalu/grpc-discovery/pkg/instance"

	"context"
	"log/slog"

	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	"google.golang.org/grpc/attributes"
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
			logger.Error("discovery updateState failed", slog.Any("err", err))
		}
	}
	if err := r.discovery.Watch(r.ctx, r.serviceName, updateState); err != nil {
		logger.Error("discovery watch failed", slog.Any("err", err))
	}
}

func buildState(services []discover.ServiceInfo) resolver.State {
	addrs := make([]resolver.Address, 0, len(services))
	attributes := &attributes.Attributes{}
	for _, s := range services {
		addrs = append(addrs, resolver.Address{
			Addr:       s.Addr,
			ServerName: s.Name,
		})
		for k, v := range s.Metadata {
			attributes = attributes.WithValue(k, v)
		}
	}
	return resolver.State{
		Addresses:  addrs,
		Attributes: attributes,
	}
}
