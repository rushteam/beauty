package router_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/governance/router"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

func newSvc(addr, version, region string) discover.ServiceInfo {
	return discover.ServiceInfo{
		ID:       addr,
		Addr:     addr,
		Metadata: map[string]string{"version": version, "region": region},
	}
}

func TestNoopRouter_PassesAll(t *testing.T) {
	r := router.NoopRouter{}
	nodes := []discover.ServiceInfo{newSvc("a", "v1", "us"), newSvc("b", "v2", "cn")}
	out := r.Filter("svc", nodes)
	if len(out) != 2 {
		t.Errorf("noop want 2, got %d", len(out))
	}
}

func TestLabelRouter_FiltersByVersion(t *testing.T) {
	f := selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2")
	r := router.NewLabelRouter(f)
	nodes := []discover.ServiceInfo{
		newSvc("a", "v1", "us"),
		newSvc("b", "v2", "cn"),
		newSvc("c", "v2", "us"),
	}
	out := r.Filter("svc", nodes)
	if len(out) != 2 {
		t.Fatalf("want 2 v2 nodes, got %d", len(out))
	}
	for _, n := range out {
		if n.Metadata["version"] != "v2" {
			t.Errorf("filtered node should be v2, got %s", n.Metadata["version"])
		}
	}
}

func TestLabelRouter_NoMatch_ReturnsEmpty(t *testing.T) {
	f := selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v3")
	r := router.NewLabelRouter(f)
	nodes := []discover.ServiceInfo{newSvc("a", "v1", "us")}
	out := r.Filter("svc", nodes)
	// fail-closed:无匹配返回空,调用方负责报错
	if len(out) != 0 {
		t.Errorf("no match should return empty (fail-closed), got %d", len(out))
	}
}

func TestLabelRouter_NilFilter_PassesAll(t *testing.T) {
	r := router.NewLabelRouter(nil)
	nodes := []discover.ServiceInfo{newSvc("a", "v1", "us")}
	out := r.Filter("svc", nodes)
	if len(out) != 1 {
		t.Errorf("nil filter want passthrough, got %d", len(out))
	}
}

func TestLabelRouter_EmptyInput(t *testing.T) {
	f := selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v1")
	r := router.NewLabelRouter(f)
	out := r.Filter("svc", nil)
	if len(out) != 0 {
		t.Errorf("empty input want empty, got %d", len(out))
	}
}

func TestChainRouter_Sequential(t *testing.T) {
	// 先按 version 过滤,再按 region 过滤
	r1 := router.NewLabelRouter(selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2"))
	r2 := router.NewLabelRouter(selector.NewLabelFilter().WithExpression("region", selector.FilterOpIn, "cn"))
	chain := router.NewChainRouter(r1, r2)
	nodes := []discover.ServiceInfo{
		newSvc("a", "v1", "cn"), // version 不符
		newSvc("b", "v2", "us"), // region 不符
		newSvc("c", "v2", "cn"), // 都符
	}
	out := chain.Filter("svc", nodes)
	if len(out) != 1 || out[0].Addr != "c" {
		t.Errorf("chain want only c, got %v", out)
	}
}

func TestChainRouter_EmptyStopsEarly(t *testing.T) {
	// 第一个 router 返回空,第二个不应被调用(短路)
	r1 := router.NewLabelRouter(selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v99"))
	called := false
	r2 := &callCountRouter{fn: func(_ string, nodes []discover.ServiceInfo) []discover.ServiceInfo {
		called = true
		return nodes
	}}
	chain := router.NewChainRouter(r1, r2)
	nodes := []discover.ServiceInfo{newSvc("a", "v1", "us")}
	out := chain.Filter("svc", nodes)
	if len(out) != 0 {
		t.Errorf("want empty, got %d", len(out))
	}
	if called {
		t.Error("chain should short-circuit on empty, r2 should not be called")
	}
}

func TestChainRouter_Empty_Passthrough(t *testing.T) {
	chain := router.NewChainRouter() // 无 router
	nodes := []discover.ServiceInfo{newSvc("a", "v1", "us")}
	out := chain.Filter("svc", nodes)
	if len(out) != 1 {
		t.Errorf("empty chain want passthrough, got %d", len(out))
	}
}

// callCountRouter 测试用 helper:记录是否被调用。
type callCountRouter struct {
	fn func(string, []discover.ServiceInfo) []discover.ServiceInfo
}

func (c *callCountRouter) Filter(name string, nodes []discover.ServiceInfo) []discover.ServiceInfo {
	return c.fn(name, nodes)
}
