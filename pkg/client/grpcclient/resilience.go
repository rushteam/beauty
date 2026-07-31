package grpcclient

import (
	mwcache "github.com/rushteam/beauty/pkg/middleware/cache"
	mwcb "github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
	"google.golang.org/grpc"
)

// WithCircuitBreakerInterceptor 接入**请求级**熔断(pkg/middleware/circuitbreaker):
// 按整体请求成败驱动熔断状态,打开时直接拒绝、快速失败。直连与服务发现两种模式均生效
// (经底层 grpc.WithChainUnaryInterceptor 接入)。
//
// 与服务发现版的 WithCircuitBreaker(节点级)互补、可叠加:
//   - 节点级(WithCircuitBreaker):selectService 选实例时跳过已熔断的节点;
//   - 请求级(本项):对逻辑调用整体熔断,直连模式也适用。
//
// gRPC 的**重试**默认已通过 service config 开启(见 DefaultRetryPolicy),无需额外接线。
// 需要注入其它一元拦截器时用 WithGRPCDialOptions(grpc.WithChainUnaryInterceptor(...))。
func WithCircuitBreakerInterceptor(cb *mwcb.CircuitBreaker) DialOption {
	return WithGRPCDialOptions(grpc.WithChainUnaryInterceptor(mwcb.UnaryClientInterceptor(cb)))
}

// WithCacheInterceptor 接入 gRPC 响应缓存(pkg/middleware/cache):
// 相同方法 + 相同请求命中缓存时直接返回,跳过网络调用。
// 拦截器注册在 chain 最外层:缓存命中不经过熔断/重试。
//
//	store := cache.NewMemoryStore(256)
//	conn, _ := grpcclient.DialContext(ctx, target,
//	    grpcclient.WithCacheInterceptor(store, cache.WithDefaultTTL(5*time.Minute)),
//	)
func WithCacheInterceptor(store mwcache.Store, opts ...mwcache.Option) DialOption {
	return WithGRPCDialOptions(grpc.WithChainUnaryInterceptor(mwcache.UnaryClientInterceptor(store, opts...)))
}
