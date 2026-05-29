package etcdv3

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func TestBuildSortedServices(t *testing.T) {
	endpoints := map[string]discover.ServiceInfo{
		"b": {ID: "b", Name: "svc"},
		"a": {ID: "a", Name: "svc"},
		"c": {ID: "c", Name: "svc"},
	}
	got := buildSortedServices(endpoints)
	want := []discover.ServiceInfo{{ID: "a", Name: "svc"}, {ID: "b", Name: "svc"}, {ID: "c", Name: "svc"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestApplyPutEvent_FilterGrpc(t *testing.T) {
	path := "/beauty/svc"
	reg := &Registry{}
	endpoints := map[string]discover.ServiceInfo{}

	mustMarshal := func(t *testing.T, s discover.ServiceInfo) []byte {
		t.Helper()
		v, err := s.Marshal()
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		return []byte(v)
	}

	// grpc
	vGrpc := discover.ServiceInfo{ID: "1", Name: "svc", Kind: "grpc"}
	services := reg.applyPutEvent(endpoints, path, path+"/1", mustMarshal(t, vGrpc))
	if len(services) != 1 || services[0].ID != "1" {
		t.Fatalf("grpc add failed: %v", services)
	}

	// non-grpc
	vNon := discover.ServiceInfo{ID: "2", Name: "svc", Kind: "http"}
	services = reg.applyPutEvent(endpoints, path, path+"/2", mustMarshal(t, vNon))
	if len(services) != 1 || services[0].ID != "1" {
		t.Fatalf("non-grpc should be filtered, got: %v", services)
	}

	// change existing to non-grpc -> remove
	vGrpc.Kind = "http"
	services = reg.applyPutEvent(endpoints, path, path+"/1", mustMarshal(t, vGrpc))
	if len(services) != 0 {
		t.Fatalf("change to non-grpc should remove, got: %v", services)
	}
}

func TestApplyDeleteEvent(t *testing.T) {
	path := "/beauty/svc"
	reg := &Registry{}
	endpoints := map[string]discover.ServiceInfo{
		"1": {ID: "1", Name: "svc", Kind: "grpc"},
		"2": {ID: "2", Name: "svc", Kind: "grpc"},
	}
	services := reg.applyDeleteEvent(endpoints, path, path+"/1")
	if len(services) != 1 || services[0].ID != "2" {
		t.Fatalf("delete failed, got: %v", services)
	}
}

func TestRegister_RejectsNonGrpc(t *testing.T) {
	reg := &Registry{}
	svc := &mockService{kind: "http", name: "api", id: "1", addr: "127.0.0.1:8080"}
	stop, err := reg.Register(context.Background(), svc)
	if err == nil {
		stop()
		t.Fatal("expected error for non-grpc service, got nil")
	}
	if stop == nil {
		t.Fatal("CancelFunc must not be nil even on error")
	}
}

func TestBuildServiceKeyAndPath(t *testing.T) {
	cases := []struct {
		prefix   string
		name     string
		id       string
		wantKey  string
		wantPath string
	}{
		{"beauty", "svc", "id1", "/beauty/svc/id1", "/beauty/svc"},
		{"/beauty", "svc", "id1", "/beauty/svc/id1", "/beauty/svc"},
		{"//beauty", "svc", "id1", "//beauty/svc/id1", "//beauty/svc"}, // TrimPrefix only strips one leading slash
	}
	for _, tc := range cases {
		gotKey := buildServiceKey(tc.prefix, tc.name, tc.id)
		gotPath := buildServicePath(tc.prefix, tc.name)
		if gotKey != tc.wantKey {
			t.Errorf("buildServiceKey(%q,%q,%q) = %q, want %q", tc.prefix, tc.name, tc.id, gotKey, tc.wantKey)
		}
		if gotPath != tc.wantPath {
			t.Errorf("buildServicePath(%q,%q) = %q, want %q", tc.prefix, tc.name, gotPath, tc.wantPath)
		}
	}
}

func TestNewRegistry_Singleton(t *testing.T) {
	// 清理全局 instance，避免污染其他测试
	mu.Lock()
	saved := instance
	instance = make(map[string]*Registry)
	mu.Unlock()
	defer func() {
		mu.Lock()
		instance = saved
		mu.Unlock()
	}()

	c := &Config{
		Endpoints: []string{"127.0.0.1:2379"},
		Prefix:    "beauty",
		DialMS:    100,
	}

	// NewRegistry 在无法连接时仍会返回 Registry（clientv3.New 是懒连接）
	r1 := NewRegistry(c)
	r2 := NewRegistry(c)
	if r1 == nil || r2 == nil {
		t.Skip("etcd not available, skip singleton test")
	}
	if r1 != r2 {
		t.Fatalf("NewRegistry should return singleton, got different pointers")
	}
}

func TestNewRegistry_Singleton_Concurrent(t *testing.T) {
	mu.Lock()
	saved := instance
	instance = make(map[string]*Registry)
	mu.Unlock()
	defer func() {
		mu.Lock()
		instance = saved
		mu.Unlock()
	}()

	c := &Config{
		Endpoints: []string{"127.0.0.1:2379"},
		Prefix:    "beauty",
		DialMS:    100,
	}

	const n = 20
	results := make([]*Registry, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = NewRegistry(c)
		}(i)
	}
	wg.Wait()

	var first *Registry
	for _, r := range results {
		if r == nil {
			t.Skip("etcd not available, skip concurrent singleton test")
		}
		if first == nil {
			first = r
		} else if r != first {
			t.Fatalf("concurrent NewRegistry returned different instances")
		}
	}
}

func TestApplyPutEvent_UnmarshalError(t *testing.T) {
	path := "/beauty/svc"
	reg := &Registry{}
	endpoints := map[string]discover.ServiceInfo{}

	// 传入非法 JSON，applyPutEvent 应跳过并返回当前 endpoints（空）
	services := reg.applyPutEvent(endpoints, path, path+"/1", []byte("not-json"))
	if len(services) != 0 {
		t.Fatalf("expected empty services on unmarshal error, got: %v", services)
	}
}

func TestGetInstanceFromKey(t *testing.T) {
	cases := []struct {
		key    string
		prefix string
		want   string
	}{
		{"/beauty/svc/abc-123", "/beauty/svc", "abc-123"},
		{"/beauty/svc/", "/beauty/svc", ""},
		{"/beauty/svc/a/b", "/beauty/svc", "a/b"}, // 多层级时返回剩余路径
	}
	for _, tc := range cases {
		got := getInstanceFromKey(tc.key, tc.prefix)
		if got != tc.want {
			t.Errorf("getInstanceFromKey(%q, %q) = %q, want %q", tc.key, tc.prefix, got, tc.want)
		}
	}
}

// mockService 实现 discover.Service 接口，用于测试
type mockService struct {
	id, name, kind, addr string
	metadata             map[string]string
}

func (m *mockService) ID() string                  { return m.id }
func (m *mockService) Name() string                { return m.name }
func (m *mockService) Kind() string                { return m.kind }
func (m *mockService) Addr() string                { return m.addr }
func (m *mockService) Metadata() map[string]string {
	if m.metadata == nil {
		return map[string]string{}
	}
	return m.metadata
}

// Ensure mockService satisfies the interface at compile time
var _ discover.Service = (*mockService)(nil)

func TestBuildServiceKey_NoPanic(t *testing.T) {
	// 各种边界 prefix 不应 panic
	for _, prefix := range []string{"", "/", "//", "beauty", "/beauty", "/beauty/"} {
		got := buildServiceKey(prefix, "svc", "id")
		if got == "" {
			t.Errorf("buildServiceKey(%q) returned empty string", prefix)
		}
		_ = fmt.Sprintf("key=%s", got) // 确保可打印
	}
}
