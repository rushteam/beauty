package resty

import (
	"context"
	"fmt"
	"testing"

	"github.com/rushteam/beauty/pkg/governance/bannednodes"
	"github.com/rushteam/beauty/pkg/service/discover"
)

func BenchmarkSelectService(b *testing.B) {
	for _, tc := range []struct {
		name     string
		strategy HTTPBalanceStrategy
	}{
		{"RoundRobin", HTTPRoundRobin},
		{"WeightedRoundRobin", HTTPWeightedRoundRobin},
		{"Random", HTTPRandom},
	} {
		b.Run(tc.name, func(b *testing.B) {
			services := make([]discover.ServiceInfo, 10)
			for i := range services {
				services[i] = discover.ServiceInfo{Addr: fmt.Sprintf("10.0.0.%d:80", i), Metadata: map[string]string{"weight": "100"}}
			}
			t := NewDiscoveryTransport(nil, "svc", WithHTTPStrategy(tc.strategy)).(*discoveryTransport)
			t.services = services
			t.rebuildBalancers(services)
			// 与 RoundTrip 一致：入口注入空的 bannednodes 列表
			ctx := bannednodes.WithBannedNodes(context.Background())
			b.ReportAllocs()
			for b.Loop() {
				if t.selectService(ctx, services) == nil {
					b.Fatal("no service selected")
				}
			}
		})
	}
}
