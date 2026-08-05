package kratos

import (
	"encoding/json"
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func TestCodec_Accept(t *testing.T) {
	c := NewCodec()
	if !c.Accept(discover.ServiceInfo{Kind: "grpc"}) {
		t.Error("should accept kind=grpc")
	}
	if !c.Accept(discover.ServiceInfo{Metadata: map[string]string{"kind": "grpc"}}) {
		t.Error("should accept metadata.kind=grpc")
	}
	if c.Accept(discover.ServiceInfo{Kind: "http"}) {
		t.Error("should reject kind=http")
	}
	if c.Accept(discover.ServiceInfo{}) {
		t.Error("should reject empty ServiceInfo")
	}
}

func TestKVCodec_BuildKey(t *testing.T) {
	c := NewKVCodec("")
	cases := []struct {
		name, id, wantKey, wantPrefix string
	}{
		{"svc", "id1", "/microservices/svc/id1", "/microservices/svc"},
		{"user.rpc", "abc", "/microservices/user.rpc/abc", "/microservices/user.rpc"},
	}
	for _, tc := range cases {
		if got := c.BuildKey(tc.name, tc.id); got != tc.wantKey {
			t.Errorf("BuildKey(%q,%q) = %q, want %q", tc.name, tc.id, got, tc.wantKey)
		}
		if got := c.BuildWatchPrefix(tc.name); got != tc.wantPrefix {
			t.Errorf("BuildWatchPrefix(%q) = %q, want %q", tc.name, got, tc.wantPrefix)
		}
	}
}

func TestKVCodec_BuildKey_CustomNamespace(t *testing.T) {
	c := NewKVCodec("myns")
	if got := c.BuildKey("svc", "id"); got != "/myns/svc/id" {
		t.Errorf("got %q, want /myns/svc/id", got)
	}
}

func TestKVCodec_MarshalValue(t *testing.T) {
	c := NewKVCodec("")
	svc := &testService{id: "1", name: "svc", kind: "grpc", addr: "10.0.1.5:9000"}
	val, err := c.MarshalValue(svc)
	if err != nil {
		t.Fatalf("MarshalValue: %v", err)
	}

	var inst kratosServiceInstance
	if err := json.Unmarshal([]byte(val), &inst); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(inst.Endpoints) != 1 || inst.Endpoints[0] != "grpc://10.0.1.5:9000" {
		t.Errorf("endpoints = %v, want [grpc://10.0.1.5:9000]", inst.Endpoints)
	}
	if inst.ID != "1" || inst.Name != "svc" {
		t.Errorf("unexpected: %+v", inst)
	}
}

func TestKVCodec_UnmarshalValue_GrpcEndpoint(t *testing.T) {
	c := NewKVCodec("")
	val := `{"id":"1","name":"svc","version":"v1","endpoints":["grpc://10.0.1.5:9000","http://10.0.1.5:8000"]}`
	info, err := c.UnmarshalValue([]byte(val), "svc")
	if err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if info.Addr != "10.0.1.5:9000" {
		t.Errorf("addr = %q, want 10.0.1.5:9000", info.Addr)
	}
	if info.Kind != "grpc" {
		t.Errorf("kind = %q, want grpc", info.Kind)
	}
	if info.Metadata["version"] != "v1" {
		t.Errorf("version = %q, want v1", info.Metadata["version"])
	}
}

func TestKVCodec_UnmarshalValue_HttpOnlyEndpoint(t *testing.T) {
	c := NewKVCodec("")
	val := `{"id":"1","name":"svc","endpoints":["http://10.0.1.5:8000"]}`
	info, err := c.UnmarshalValue([]byte(val), "svc")
	if err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if info.Addr != "10.0.1.5:8000" {
		t.Errorf("addr = %q, want 10.0.1.5:8000", info.Addr)
	}
	if info.Kind != "http" {
		t.Errorf("kind = %q, want http", info.Kind)
	}
}

func TestKVCodec_UnmarshalValue_NoEndpoints(t *testing.T) {
	c := NewKVCodec("")
	val := `{"id":"1","name":"svc","endpoints":[]}`
	if _, err := c.UnmarshalValue([]byte(val), "svc"); err == nil {
		t.Error("should reject empty endpoints")
	}
}

func TestKVCodec_UnmarshalValue_InvalidJSON(t *testing.T) {
	c := NewKVCodec("")
	if _, err := c.UnmarshalValue([]byte("not-json"), "svc"); err == nil {
		t.Error("should reject non-JSON value")
	}
}

func TestKVCodec_RoundTrip(t *testing.T) {
	c := NewKVCodec("")
	svc := &testService{
		id:   "uuid-123",
		name: "user.rpc",
		kind: "grpc",
		addr: "10.0.1.5:9000",
		metadata: map[string]string{
			"version": "v2",
			"region":  "us-east",
		},
	}

	val, err := c.MarshalValue(svc)
	if err != nil {
		t.Fatalf("MarshalValue: %v", err)
	}

	info, err := c.UnmarshalValue([]byte(val), "user.rpc")
	if err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if info.ID != "uuid-123" || info.Name != "user.rpc" || info.Addr != "10.0.1.5:9000" || info.Kind != "grpc" {
		t.Errorf("round-trip basic fields failed: %+v", info)
	}
	if info.Metadata["version"] != "v2" || info.Metadata["region"] != "us-east" {
		t.Errorf("round-trip metadata failed: %+v", info.Metadata)
	}
}

func TestKVCodec_Accept(t *testing.T) {
	c := NewKVCodec("")
	if !c.Accept(discover.ServiceInfo{Kind: "grpc"}) {
		t.Error("should accept grpc")
	}
	if c.Accept(discover.ServiceInfo{Kind: "http"}) {
		t.Error("should reject http")
	}
}

type testService struct {
	id, name, kind, addr string
	metadata             map[string]string
}

func (s *testService) ID() string                  { return s.id }
func (s *testService) Name() string                { return s.name }
func (s *testService) Kind() string                { return s.kind }
func (s *testService) Addr() string                { return s.addr }
func (s *testService) Metadata() map[string]string { return s.metadata }
