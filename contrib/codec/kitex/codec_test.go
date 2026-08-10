package kitex

import (
	"encoding/json"
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
)

type mockService struct {
	id, name, kind, addr string
	metadata             map[string]string
}

func (s *mockService) ID() string                  { return s.id }
func (s *mockService) Name() string                { return s.name }
func (s *mockService) Kind() string                { return s.kind }
func (s *mockService) Addr() string                { return s.addr }
func (s *mockService) Metadata() map[string]string { return s.metadata }

func TestCodecAcceptAll(t *testing.T) {
	c := NewCodec()
	if !c.Accept(discover.ServiceInfo{Kind: "grpc"}) {
		t.Error("should accept grpc")
	}
	if !c.Accept(discover.ServiceInfo{Kind: "thrift"}) {
		t.Error("should accept thrift")
	}
	if !c.Accept(discover.ServiceInfo{}) {
		t.Error("should accept empty kind")
	}
}

func TestKVCodecBuildKey(t *testing.T) {
	c := NewKVCodec("")
	key := c.BuildKey("example.shop.item", "10.0.1.5:8888")
	want := "kitex/registry-etcd/example.shop.item10.0.1.5:8888"
	if key != want {
		t.Errorf("BuildKey = %q, want %q", key, want)
	}
}

func TestKVCodecBuildKeyCustomPrefix(t *testing.T) {
	c := NewKVCodec("my-prefix")
	key := c.BuildKey("svc", "1.2.3.4:9999")
	want := "my-prefix/svc1.2.3.4:9999"
	if key != want {
		t.Errorf("BuildKey = %q, want %q", key, want)
	}
}

func TestKVCodecBuildWatchPrefix(t *testing.T) {
	c := NewKVCodec("")
	prefix := c.BuildWatchPrefix("example.shop.item")
	want := "kitex/registry-etcd/example.shop.item"
	if prefix != want {
		t.Errorf("BuildWatchPrefix = %q, want %q", prefix, want)
	}
}

func TestKVCodecMarshalValue(t *testing.T) {
	c := NewKVCodec("")
	svc := &mockService{
		id:       "abc",
		name:     "example.shop.item",
		kind:     "thrift",
		addr:     "10.0.1.5:8888",
		metadata: map[string]string{"weight": "200", "env": "prod"},
	}
	val, err := c.MarshalValue(svc)
	if err != nil {
		t.Fatalf("MarshalValue error: %v", err)
	}

	var inst instanceInfo
	if err := json.Unmarshal([]byte(val), &inst); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if inst.Network != "tcp" {
		t.Errorf("network = %q, want tcp", inst.Network)
	}
	if inst.Address != "10.0.1.5:8888" {
		t.Errorf("address = %q, want 10.0.1.5:8888", inst.Address)
	}
	if inst.Weight != 200 {
		t.Errorf("weight = %d, want 200", inst.Weight)
	}
}

func TestKVCodecUnmarshalValue(t *testing.T) {
	c := NewKVCodec("")
	data := `{"network":"tcp","address":"10.0.1.5:8888","weight":150,"tags":{"env":"staging"}}`
	info, err := c.UnmarshalValue([]byte(data), "example.shop.item")
	if err != nil {
		t.Fatalf("UnmarshalValue error: %v", err)
	}
	if info.Kind != "thrift" {
		t.Errorf("kind = %q, want thrift", info.Kind)
	}
	if info.Name != "example.shop.item" {
		t.Errorf("name = %q, want example.shop.item", info.Name)
	}
	if info.Addr != "10.0.1.5:8888" {
		t.Errorf("addr = %q, want 10.0.1.5:8888", info.Addr)
	}
	if info.Metadata["weight"] != "150" {
		t.Errorf("weight = %q, want 150", info.Metadata["weight"])
	}
	if info.Metadata["env"] != "staging" {
		t.Errorf("env = %q, want staging", info.Metadata["env"])
	}
}

func TestKVCodecRoundTrip(t *testing.T) {
	c := NewKVCodec("")
	svc := &mockService{
		id:       "node1",
		name:     "my.svc",
		kind:     "thrift",
		addr:     "192.168.1.1:9000",
		metadata: map[string]string{"weight": "80"},
	}
	val, err := c.MarshalValue(svc)
	if err != nil {
		t.Fatalf("MarshalValue: %v", err)
	}
	info, err := c.UnmarshalValue([]byte(val), "my.svc")
	if err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if info.Addr != "192.168.1.1:9000" {
		t.Errorf("round-trip addr = %q, want 192.168.1.1:9000", info.Addr)
	}
	if info.Metadata["weight"] != "80" {
		t.Errorf("round-trip weight = %q, want 80", info.Metadata["weight"])
	}
}
