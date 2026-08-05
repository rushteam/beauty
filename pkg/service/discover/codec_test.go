package discover

import "testing"

func TestBeautyCodec_Accept(t *testing.T) {
	c := NewBeautyCodec()
	if !c.Accept(ServiceInfo{Kind: "grpc"}) {
		t.Error("should accept kind=grpc")
	}
	if !c.Accept(ServiceInfo{Metadata: map[string]string{"kind": "grpc"}}) {
		t.Error("should accept metadata.kind=grpc")
	}
	if c.Accept(ServiceInfo{Kind: "http"}) {
		t.Error("should reject kind=http")
	}
	if c.Accept(ServiceInfo{}) {
		t.Error("should reject empty ServiceInfo")
	}
}

func TestBeautyKVCodec_BuildKey(t *testing.T) {
	c := NewBeautyKVCodec("beauty")
	cases := []struct {
		name, id, wantKey, wantPrefix string
	}{
		{"svc", "id1", "/beauty/svc/id1", "/beauty/svc"},
		{"user.rpc", "abc", "/beauty/user.rpc/abc", "/beauty/user.rpc"},
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

func TestBeautyKVCodec_LeadingSlashPrefix(t *testing.T) {
	c := NewBeautyKVCodec("/beauty")
	if got := c.BuildKey("svc", "id"); got != "/beauty/svc/id" {
		t.Errorf("got %q, want /beauty/svc/id", got)
	}
}

func TestBeautyKVCodec_MarshalUnmarshal(t *testing.T) {
	c := NewBeautyKVCodec("beauty")
	svc := &testService{id: "1", name: "svc", kind: "grpc", addr: "10.0.1.5:8080"}

	val, err := c.MarshalValue(svc)
	if err != nil {
		t.Fatalf("MarshalValue: %v", err)
	}
	info, err := c.UnmarshalValue([]byte(val), "svc")
	if err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if info.Addr != "10.0.1.5:8080" || info.Kind != "grpc" {
		t.Errorf("round-trip failed: %+v", info)
	}
}

func TestBeautyKVCodec_UnmarshalRejectsPlainString(t *testing.T) {
	c := NewBeautyKVCodec("beauty")
	if _, err := c.UnmarshalValue([]byte("not-json"), "svc"); err == nil {
		t.Error("should reject non-JSON value")
	}
}

// testService implements Service for testing
type testService struct {
	id, name, kind, addr string
	metadata             map[string]string
}

func (s *testService) ID() string                  { return s.id }
func (s *testService) Name() string                { return s.name }
func (s *testService) Kind() string                { return s.kind }
func (s *testService) Addr() string                { return s.addr }
func (s *testService) Metadata() map[string]string { return s.metadata }
