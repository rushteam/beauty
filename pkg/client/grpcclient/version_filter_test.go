package grpcclient

import (
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

func TestWithVersionIn_NoMatch_FallbackAll(t *testing.T) {
	// 没有匹配的版本时 Filter 应容错返回全部实例
	all := makeServices("v1", "v2")
	filter := NewLabelFilter().WithVersionIn("v99")
	got := filter.Filter(all)

	if len(got) != len(all) {
		t.Errorf("no-match fallback: want %d instances, got %d", len(all), len(got))
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
