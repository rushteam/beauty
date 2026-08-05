package gozero

import (
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func TestCodec_AcceptAll(t *testing.T) {
	c := NewCodec()
	if !c.Accept(discover.ServiceInfo{}) {
		t.Error("should accept empty ServiceInfo")
	}
	if !c.Accept(discover.ServiceInfo{Kind: "http"}) {
		t.Error("should accept kind=http")
	}
	if !c.Accept(discover.ServiceInfo{Kind: "grpc"}) {
		t.Error("should accept kind=grpc")
	}
}

func TestKVCodec_BuildKey(t *testing.T) {
	c := NewKVCodec()
	cases := []struct {
		name, id, wantKey, wantPrefix string
	}{
		{"user.rpc", "12345", "user.rpc/12345", "user.rpc"},
		{"greeter.rpc", "abc", "greeter.rpc/abc", "greeter.rpc"},
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

func TestKVCodec_MarshalPlainAddr(t *testing.T) {
	c := NewKVCodec()
	svc := &testService{addr: "10.0.1.5:8080"}
	val, err := c.MarshalValue(svc)
	if err != nil {
		t.Fatalf("MarshalValue: %v", err)
	}
	if val != "10.0.1.5:8080" {
		t.Errorf("got %q, want plain addr", val)
	}
}

func TestKVCodec_UnmarshalPlainAddr(t *testing.T) {
	c := NewKVCodec()
	info, err := c.UnmarshalValue([]byte("10.0.1.5:8080"), "user.rpc")
	if err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if info.Addr != "10.0.1.5:8080" || info.Kind != "grpc" || info.Name != "user.rpc" {
		t.Errorf("unexpected: %+v", info)
	}
}

func TestKVCodec_UnmarshalAlsoAcceptsJSON(t *testing.T) {
	c := NewKVCodec()
	jsonVal := `{"id":"1","kind":"grpc","name":"svc","addr":"10.0.1.5:8080"}`
	info, err := c.UnmarshalValue([]byte(jsonVal), "svc")
	if err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if info.Addr != "10.0.1.5:8080" || info.ID != "1" {
		t.Errorf("unexpected: %+v", info)
	}
}

func TestKVCodec_UnmarshalRejectsEmpty(t *testing.T) {
	c := NewKVCodec()
	if _, err := c.UnmarshalValue([]byte(""), "svc"); err == nil {
		t.Error("should reject empty value")
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
