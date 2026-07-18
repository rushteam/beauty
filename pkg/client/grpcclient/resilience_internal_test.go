package grpcclient

import (
	"testing"

	mwcb "github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
)

// WithCircuitBreakerInterceptor 应把请求级熔断拦截器接入底层 gRPC 拨号选项,
// 且直连与服务发现两种模式共享同一 grpcOpts。
func TestWithCircuitBreakerInterceptor_WiresDialOpt(t *testing.T) {
	cb := mwcb.NewCircuitBreaker(mwcb.DefaultConfig("grpc-test"))
	cfg := &dialConfig{}
	WithCircuitBreakerInterceptor(cb)(cfg)
	if len(cfg.grpcOpts) == 0 {
		t.Fatal("WithCircuitBreakerInterceptor 应向 grpcOpts 追加拦截器拨号选项")
	}
}
