package router_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/governance/router"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

func newGeoSvc(addr, region, zone, campus string) discover.ServiceInfo {
	return discover.ServiceInfo{
		ID:   addr,
		Addr: addr,
		Metadata: map[string]string{
			"region": region,
			"zone":   zone,
			"campus": campus,
		},
	}
}

func TestLocalityRouter_MatchCampus(t *testing.T) {
	r := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-1a",
		Campus: "campus-1",
	})
	nodes := []discover.ServiceInfo{
		newGeoSvc("a", "cn-east", "cn-east-1a", "campus-1"),
		newGeoSvc("b", "cn-east", "cn-east-1a", "campus-2"),
		newGeoSvc("c", "cn-east", "cn-east-1b", "campus-3"),
	}
	out := r.Filter("svc", nodes)
	if len(out) != 1 || out[0].Addr != "a" {
		t.Errorf("campus match: want [a], got %v", addrs(out))
	}
}

func TestLocalityRouter_FallbackToZone(t *testing.T) {
	r := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-1a",
		Campus: "campus-99", // 不存在的 campus
	})
	nodes := []discover.ServiceInfo{
		newGeoSvc("a", "cn-east", "cn-east-1a", "campus-1"),
		newGeoSvc("b", "cn-east", "cn-east-1a", "campus-2"),
		newGeoSvc("c", "cn-east", "cn-east-1b", "campus-3"),
	}
	out := r.Filter("svc", nodes)
	if len(out) != 2 {
		t.Fatalf("zone fallback: want 2 nodes (a,b), got %v", addrs(out))
	}
	for _, n := range out {
		if n.Metadata["zone"] != "cn-east-1a" {
			t.Errorf("zone fallback: want zone cn-east-1a, got %s", n.Metadata["zone"])
		}
	}
}

func TestLocalityRouter_FallbackToRegion(t *testing.T) {
	r := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-1c", // 不存在的 zone
		Campus: "campus-99",
	})
	nodes := []discover.ServiceInfo{
		newGeoSvc("a", "cn-east", "cn-east-1a", "campus-1"),
		newGeoSvc("b", "us-west", "us-west-1a", "campus-1"),
	}
	out := r.Filter("svc", nodes)
	if len(out) != 1 || out[0].Addr != "a" {
		t.Errorf("region fallback: want [a], got %v", addrs(out))
	}
}

func TestLocalityRouter_NoMatch_FailClosed(t *testing.T) {
	r := router.NewLocalityRouter(router.Locality{
		Region: "eu-west",
		Zone:   "eu-west-1a",
	})
	nodes := []discover.ServiceInfo{
		newGeoSvc("a", "cn-east", "cn-east-1a", "campus-1"),
		newGeoSvc("b", "us-west", "us-west-1a", "campus-1"),
	}
	out := r.Filter("svc", nodes)
	if out != nil && len(out) != 0 {
		t.Errorf("no match without globalFallback: want nil/empty, got %v", addrs(out))
	}
}

func TestLocalityRouter_GlobalFallback(t *testing.T) {
	r := router.NewLocalityRouter(router.Locality{
		Region: "eu-west",
		Zone:   "eu-west-1a",
	}, router.WithGlobalFallback(true))
	nodes := []discover.ServiceInfo{
		newGeoSvc("a", "cn-east", "cn-east-1a", "campus-1"),
		newGeoSvc("b", "us-west", "us-west-1a", "campus-1"),
	}
	out := r.Filter("svc", nodes)
	if len(out) != 2 {
		t.Errorf("globalFallback: want all 2 nodes, got %v", addrs(out))
	}
}

func TestLocalityRouter_EmptyNodes(t *testing.T) {
	r := router.NewLocalityRouter(router.Locality{Region: "cn-east"})
	out := r.Filter("svc", nil)
	if len(out) != 0 {
		t.Errorf("empty input: want empty, got %d", len(out))
	}
	out = r.Filter("svc", []discover.ServiceInfo{})
	if len(out) != 0 {
		t.Errorf("empty slice input: want empty, got %d", len(out))
	}
}

func TestLocalityRouter_CustomTiers(t *testing.T) {
	r := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-1a",
	}, router.WithTiers("zone", "region")) // 跳过 campus
	nodes := []discover.ServiceInfo{
		newGeoSvc("a", "cn-east", "cn-east-1a", "campus-1"),
		newGeoSvc("b", "cn-east", "cn-east-1b", "campus-1"),
	}
	out := r.Filter("svc", nodes)
	// 只配了 zone/region，zone 匹配到 a
	if len(out) != 1 || out[0].Addr != "a" {
		t.Errorf("custom tiers (zone first): want [a], got %v", addrs(out))
	}
}

func TestLocalityRouter_OnlyRegionSet(t *testing.T) {
	r := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
	})
	nodes := []discover.ServiceInfo{
		newGeoSvc("a", "cn-east", "cn-east-1a", "campus-1"),
		newGeoSvc("b", "us-west", "us-west-1a", "campus-1"),
		newGeoSvc("c", "cn-east", "cn-east-1b", "campus-2"),
	}
	out := r.Filter("svc", nodes)
	// campus/zone 为空，跳过；region 命中 a, c
	if len(out) != 2 {
		t.Fatalf("only region set: want 2 nodes (a,c), got %v", addrs(out))
	}
	for _, n := range out {
		if n.Metadata["region"] != "cn-east" {
			t.Errorf("only region set: want region cn-east, got %s", n.Metadata["region"])
		}
	}
}

func TestLocalityRouter_ChainWithLabel(t *testing.T) {
	locality := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-1a",
	}, router.WithGlobalFallback(true))

	// 先 locality 过滤（zone 匹配 a,b），再 label 过滤（version=v2 只留 b）
	nodes := []discover.ServiceInfo{
		{ID: "a", Addr: "a", Metadata: map[string]string{
			"region": "cn-east", "zone": "cn-east-1a", "campus": "", "version": "v1",
		}},
		{ID: "b", Addr: "b", Metadata: map[string]string{
			"region": "cn-east", "zone": "cn-east-1a", "campus": "", "version": "v2",
		}},
		{ID: "c", Addr: "c", Metadata: map[string]string{
			"region": "us-west", "zone": "us-west-1a", "campus": "", "version": "v2",
		}},
	}

	labelFilter := router.NewLabelRouter(
		selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2"),
	)
	chain := router.NewChainRouter(locality, labelFilter)
	out := chain.Filter("svc", nodes)
	if len(out) != 1 || out[0].Addr != "b" {
		t.Errorf("chain locality+label: want [b], got %v", addrs(out))
	}
}

func addrs(nodes []discover.ServiceInfo) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Addr
	}
	return out
}
