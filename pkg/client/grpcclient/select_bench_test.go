package grpcclient

import (
	"context"
	"fmt"
	"testing"

	"github.com/rushteam/beauty/pkg/governance/bannednodes"
	"github.com/rushteam/beauty/pkg/service/discover"
)

func benchServices(n int) []discover.ServiceInfo {
	ss := make([]discover.ServiceInfo, n)
	for i := range ss {
		ss[i] = discover.ServiceInfo{Addr: fmt.Sprintf("10.0.0.%d:80", i), Metadata: map[string]string{"weight": "100"}}
	}
	return ss
}

func BenchmarkSelectService(b *testing.B) {
	for _, tc := range []struct {
		name     string
		strategy LoadBalanceStrategy
	}{
		{"RoundRobin", RoundRobin},
		{"WeightedRoundRobin", WeightedRoundRobin},
		{"Random", Random},
	} {
		b.Run(tc.name, func(b *testing.B) {
			services := benchServices(10)
			c := NewServiceDiscoveryClient(nil, "svc", WithDiscoveryStrategy(tc.strategy), WithDiscoveryHealthCheck(false, 0))
			c.services = services
			c.rebuildBalancers(services)
			// 与 Call 一致：重试链入口注入空的 bannednodes 列表
			ctx := bannednodes.WithBannedNodes(context.Background())
			b.ReportAllocs()
			for b.Loop() {
				if c.selectService(ctx, services) == nil {
					b.Fatal("no service selected")
				}
			}
		})
	}
}
