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

func TestBuildState_AttributesEqualNoPanic(t *testing.T) {
	// 回归：gRPC attributes.Equal 对不可比较类型回落 != 会 panic。
	// 用相同 / 不同 metadata 各比一次，确保包装类型可安全比较。
	s1 := buildState([]discover.ServiceInfo{
		{Addr: "10.0.0.1:8080", Metadata: map[string]string{"env": "prod", "kind": "grpc"}},
	})
	s2 := buildState([]discover.ServiceInfo{
		{Addr: "10.0.0.1:8080", Metadata: map[string]string{"env": "prod", "kind": "grpc"}},
	})
	s3 := buildState([]discover.ServiceInfo{
		{Addr: "10.0.0.1:8080", Metadata: map[string]string{"env": "staging", "kind": "grpc"}},
	})

	a1, a2, a3 := s1.Addresses[0], s2.Addresses[0], s3.Addresses[0]
	if !a1.Attributes.Equal(a2.Attributes) {
		t.Fatal("same metadata should Equal")
	}
	if a1.Attributes.Equal(a3.Attributes) {
		t.Fatal("different metadata should not Equal")
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
