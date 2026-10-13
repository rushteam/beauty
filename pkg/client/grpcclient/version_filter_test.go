package grpcclient

import (
	"strings"
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func makeServices(versions ...string) []discover.ServiceInfo {
	svcs := make([]discover.ServiceInfo, len(versions))
	for i, v := range versions {
		svcs[i] = discover.ServiceInfo{
			ID:   "svc-" + v,
			Name: "test-svc",
			Addr: "127.0.0.1:900" + v,
			Metadata: map[string]string{
				"kind":    "grpc",
				"version": v,
			},
		}
	}
	return svcs
}

func TestWithVersionIn_FilterService(t *testing.T) {
	all := makeServices("v1", "v2", "v3")

	filter := NewLabelFilter().WithVersionIn("v2")
	got := filter.Filter(all)

	if len(got) != 1 {
		t.Fatalf("want 1 instance, got %d", len(got))
	}
	if got[0].Metadata["version"] != "v2" {
		t.Errorf("want version=v2, got %s", got[0].Metadata["version"])
	}
}

func TestWithVersionIn_MultiVersion(t *testing.T) {
	all := makeServices("v1", "v2", "v3")

	filter := NewLabelFilter().WithVersionIn("v1", "v3")
	got := filter.Filter(all)

	if len(got) != 2 {
		t.Fatalf("want 2 instances, got %d", len(got))
	}
	versions := map[string]bool{}
	for _, s := range got {
		versions[s.Metadata["version"]] = true
	}
	if !versions["v1"] || !versions["v3"] {
		t.Errorf("want v1 and v3, got %v", versions)
	}
}

func TestWithVersionIn_NoMatch_FailClosed(t *testing.T) {
	// 没有匹配的版本时 Filter 应 fail-closed 返回空，而不是退回全部，
	// 否则会静默击穿版本/地域隔离（路由到不兼容实例）。调用方据空结果报错。
	all := makeServices("v1", "v2")
	filter := NewLabelFilter().WithVersionIn("v99")
	got := filter.Filter(all)

	if len(got) != 0 {
		t.Errorf("no-match should fail-closed: want 0 instances, got %d", len(got))
	}
}

func TestWithVersionIn_CombineWithEnvironment(t *testing.T) {
	svcs := []discover.ServiceInfo{
		{ID: "a", Metadata: map[string]string{"version": "v2", "environment": "prod"}},
		{ID: "b", Metadata: map[string]string{"version": "v2", "environment": "staging"}},
		{ID: "c", Metadata: map[string]string{"version": "v1", "environment": "prod"}},
	}

	filter := NewLabelFilter().
		WithVersionIn("v2").
		WithEnvironmentIn("prod")

	got := filter.Filter(svcs)
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("combined filter: want [a], got %v", got)
	}
}

func TestBuildFilterFromParams_Version(t *testing.T) {
	params := map[string]string{"version": "v2,v3"}
	filter := buildFilterFromParams(params)
	if filter == nil {
		t.Fatal("expected non-nil filter for version param")
	}

	all := makeServices("v1", "v2", "v3")
	got := filter.Filter(all)
	if len(got) != 2 {
		t.Errorf("URL version param: want 2 instances, got %d", len(got))
	}
}

// TestBuildFilterFromParams_ConfigKeysNotLabels 回归:白名单设计下,注册中心/连接
// 配置键不会被当成 metadata 标签。典型例子 ?codec=gozero 不应产生任何过滤器,
// 否则 metadata 为空的 go-zero 实例会被全部滤掉。
func TestBuildFilterFromParams_ConfigKeysNotLabels(t *testing.T) {
	// 仅含配置键(非白名单参数)时不应产生任何过滤器(nil),所有实例应被放行。
	params := map[string]string{
		"codec": "gozero", "ttl": "15", "namespace": "public",
		"token": "x", "weight": "10", "protocol": "grpc", "tenant": "acme",
	}
	if filter := buildFilterFromParams(params); filter != nil {
		t.Fatalf("non-whitelisted params should yield nil filter, got %#v", filter)
	}

	// go-zero 场景:实例 metadata 为空,?codec=gozero 不应将其滤掉。
	gozeroInstances := []discover.ServiceInfo{
		{ID: "gz-1", Name: "test-svc", Addr: "127.0.0.1:9001"},
		{ID: "gz-2", Name: "test-svc", Addr: "127.0.0.1:9002"},
	}
	if filter := buildFilterFromParams(map[string]string{"codec": "gozero"}); filter != nil {
		if got := filter.Filter(gozeroInstances); len(got) != len(gozeroInstances) {
			t.Fatalf("?codec=gozero must not filter empty-metadata instances: want %d, got %d",
				len(gozeroInstances), len(got))
		}
	}
}

// TestBuildFilterFromParams_Selector 确认 metadata 过滤走 k8s 风格的 ?selector= 参数,
// 等值 + 集合算子组合可正确筛选。
func TestBuildFilterFromParams_Selector(t *testing.T) {
	filter := buildFilterFromParams(map[string]string{
		"selector": "tenant=acme,version in (v2,v3),!deprecated",
	})
	if filter == nil {
		t.Fatal("expected non-nil filter for selector param")
	}
	svcs := []discover.ServiceInfo{
		{ID: "a", Metadata: map[string]string{"tenant": "acme", "version": "v2"}},
		{ID: "b", Metadata: map[string]string{"tenant": "acme", "version": "v1"}},                       // version 不在集合
		{ID: "c", Metadata: map[string]string{"tenant": "other", "version": "v2"}},                      // tenant 不等
		{ID: "d", Metadata: map[string]string{"tenant": "acme", "version": "v3", "deprecated": "true"}}, // 含 deprecated
	}
	got := filter.Filter(svcs)
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("selector filter: want [a], got %v", got)
	}

	// labels / label 作为别名走同一解析器。
	if f := buildFilterFromParams(map[string]string{"labels": "tenant=acme"}); f == nil {
		t.Error("expected non-nil filter for labels alias")
	}
	if f := buildFilterFromParams(map[string]string{"label": "tenant=acme"}); f == nil {
		t.Error("expected non-nil filter for label alias")
	}
}

// TestParseSelectorInto 覆盖 k8s selector 各算子的解析与匹配。
func TestParseSelectorInto(t *testing.T) {
	svcs := []discover.ServiceInfo{
		{ID: "a", Metadata: map[string]string{"env": "prod", "tier": "web", "version": "v2"}},
		{ID: "b", Metadata: map[string]string{"env": "staging", "tier": "web", "version": "v1"}},
		{ID: "c", Metadata: map[string]string{"env": "prod", "version": "v3", "canary": "1"}},
	}
	tests := []struct {
		name     string
		selector string
		wantIDs  []string
	}{
		{"equals", "env=prod", []string{"a", "c"}},
		{"double-equals", "env==prod", []string{"a", "c"}},
		{"not-equals", "env!=prod", []string{"b"}},
		{"in", "version in (v2,v3)", []string{"a", "c"}},
		{"notin", "version notin (v1)", []string{"a", "c"}},
		{"exists", "tier", []string{"a", "b"}},
		{"not-exists", "!tier", []string{"c"}},
		{"combined", "env=prod,version in (v2,v3),!canary", []string{"a"}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewLabelFilter()
			applied := parseSelectorInto(f, tt.selector)
			if tt.wantIDs == nil {
				if applied {
					t.Fatalf("empty selector should not apply any expression")
				}
				return
			}
			if !applied {
				t.Fatalf("selector %q applied nothing", tt.selector)
			}
			got := f.Filter(svcs)
			var ids []string
			for _, s := range got {
				ids = append(ids, s.ID)
			}
			if len(ids) != len(tt.wantIDs) {
				t.Fatalf("selector %q: got %v, want %v", tt.selector, ids, tt.wantIDs)
			}
			for i, id := range tt.wantIDs {
				if ids[i] != id {
					t.Errorf("selector %q: got %v, want %v", tt.selector, ids, tt.wantIDs)
					break
				}
			}
		})
	}
}

func TestSplitSelectorTerms(t *testing.T) {
	// 括号内的逗号不应作为条件分隔符。
	got := splitSelectorTerms("a=1,b in (x,y,z),!c")
	want := []string{"a=1", "b in (x,y,z)", "!c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			t.Errorf("term %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDialConfig_WithVersion(t *testing.T) {
	cfg := &dialConfig{}
	WithVersion("v2")(cfg)
	if len(cfg.versions) != 1 || cfg.versions[0] != "v2" {
		t.Errorf("WithVersion: want [v2], got %v", cfg.versions)
	}
}

func TestDialConfig_WithVersionIn(t *testing.T) {
	cfg := &dialConfig{}
	WithVersionIn("v1", "v2")(cfg)
	if len(cfg.versions) != 2 {
		t.Errorf("WithVersionIn: want 2 versions, got %v", cfg.versions)
	}
}
