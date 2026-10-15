package grpcclient

import (
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func TestMetadataAttr_Equal(t *testing.T) {
	a := metadataAttr{"env": "prod", "version": "v1"}
	b := metadataAttr{"env": "prod", "version": "v1"}
	c := metadataAttr{"env": "staging", "version": "v1"}
	d := metadataAttr{"env": "prod"}

	if !a.Equal(b) {
		t.Fatal("identical maps should be equal")
	}
	if a.Equal(c) {
		t.Fatal("different values should not be equal")
	}
	if a.Equal(d) {
		t.Fatal("different lengths should not be equal")
	}
	if a.Equal(map[string]string{"env": "prod", "version": "v1"}) {
		t.Fatal("raw map should not equal metadataAttr")
	}
	if a.Equal(nil) {
		t.Fatal("nil should not equal metadataAttr")
	}
	if !(metadataAttr(nil)).Equal(metadataAttr(nil)) {
		t.Fatal("two nil metadataAttr should be equal")
	}
}

func TestBuildState_UsesBalancerAttributes(t *testing.T) {
	meta := map[string]string{"env": "prod", "kind": "grpc", "weight": "100"}
	state := buildState([]discover.ServiceInfo{
		{Addr: "10.0.0.1:8080", Metadata: meta},
	})
	a := state.Addresses[0]

	// 服务发现 metadata 属于 LB 信息，不应进 Attributes（避免 pickfirst 当成 SubConn 身份）。
	if a.Attributes != nil {
		t.Fatal("Attributes should be nil for discovery metadata")
	}
	if a.BalancerAttributes == nil {
		t.Fatal("BalancerAttributes should hold discovery metadata")
	}
	if !a.BalancerAttributes.Equal(a.BalancerAttributes) {
		t.Fatal("BalancerAttributes.Equal self should succeed")
	}

	// metadata 变更不应影响 Attributes 相等（两者都为 nil）。
	other := buildState([]discover.ServiceInfo{
		{Addr: "10.0.0.1:8080", Metadata: map[string]string{"env": "staging"}},
	}).Addresses[0]
	if !a.Attributes.Equal(other.Attributes) {
		t.Fatal("nil Attributes should remain equal across metadata-only changes")
	}
	if a.BalancerAttributes.Equal(other.BalancerAttributes) {
		t.Fatal("different BalancerAttributes metadata should not Equal")
	}
}

func TestBuildState_MetadataCloned(t *testing.T) {
	meta := map[string]string{"env": "prod"}
	state := buildState([]discover.ServiceInfo{
		{Addr: "10.0.0.1:8080", Metadata: meta},
	})
	meta["env"] = "mutated"
	got := extractMetadata(state.Addresses[0])
	if got["env"] != "prod" {
		t.Fatalf("BalancerAttributes should own a clone, got %q", got["env"])
	}
}

func TestExtractMetadata_RoundTrip(t *testing.T) {
	meta := map[string]string{"environment": "prod", "version": "v2"}
	state := buildState([]discover.ServiceInfo{
		{Addr: "10.0.0.1:8080", Metadata: meta},
		{Addr: "10.0.0.2:8080"}, // 无 metadata
	})

	got := extractMetadata(state.Addresses[0])
	if got["environment"] != "prod" || got["version"] != "v2" {
		t.Fatalf("extractMetadata = %v, want %v", got, meta)
	}
	if extractMetadata(state.Addresses[1]) != nil {
		t.Fatal("empty metadata should extract as nil")
	}
}
