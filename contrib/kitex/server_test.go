package kitex

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func TestNewServer(t *testing.T) {
	srv := New(":18888", WithServiceName("test.service"))

	if srv.ID() == "" {
		t.Error("ID should not be empty")
	}
	if srv.Name() != "test.service" {
		t.Errorf("Name = %q, want test.service", srv.Name())
	}
	if srv.Kind() != "thrift" {
		t.Errorf("Kind = %q, want thrift", srv.Kind())
	}
	if srv.Server() == nil {
		t.Error("Server() should not be nil")
	}
}

func TestDefaultMetadata(t *testing.T) {
	srv := New(":18889")
	md := srv.Metadata()
	if md["kind"] != "thrift" {
		t.Errorf("kind = %q, want thrift", md["kind"])
	}
}

func TestWithOptions(t *testing.T) {
	srv := New(":18890",
		WithServiceName("my.svc"),
		WithWeight(200),
		WithVersion("v1.2.0"),
		WithEnvironment("staging"),
		WithRegionInfo("cn-east", "shanghai", "campus1"),
		WithMetadata(map[string]string{"custom": "value"}),
	)

	if srv.Name() != "my.svc" {
		t.Errorf("Name = %q, want my.svc", srv.Name())
	}
	md := srv.Metadata()
	if md["weight"] != "200" {
		t.Errorf("weight = %q, want 200", md["weight"])
	}
	if md["version"] != "v1.2.0" {
		t.Errorf("version = %q, want v1.2.0", md["version"])
	}
	if md["environment"] != "staging" {
		t.Errorf("environment = %q, want staging", md["environment"])
	}
	if md["region"] != "cn-east" {
		t.Errorf("region = %q, want cn-east", md["region"])
	}
	if md["zone"] != "shanghai" {
		t.Errorf("zone = %q, want shanghai", md["zone"])
	}
	if md["campus"] != "campus1" {
		t.Errorf("campus = %q, want campus1", md["campus"])
	}
	if md["custom"] != "value" {
		t.Errorf("custom = %q, want value", md["custom"])
	}
}

func TestDiscoverServiceInterface(t *testing.T) {
	srv := New(":18891", WithServiceName("discover.test"))
	var svc discover.Service = srv
	if svc.ID() == "" {
		t.Error("ID should not be empty")
	}
	if svc.Name() != "discover.test" {
		t.Errorf("Name = %q, want discover.test", svc.Name())
	}
	if svc.Kind() != "thrift" {
		t.Errorf("Kind = %q, want thrift", svc.Kind())
	}
}

func TestReadyNotifier(t *testing.T) {
	srv := New(":18892")
	ch := srv.Ready()
	if ch == nil {
		t.Error("Ready() should return a non-nil channel")
	}
}

func TestThriftServiceWrapper(t *testing.T) {
	w := &thriftServiceWrapper{
		id:          "test-id",
		serviceName: "test.svc",
		addr:        "10.0.0.1:8888",
		metadata:    map[string]string{"key": "val"},
	}
	if w.ID() != "test-id" {
		t.Errorf("ID = %q, want test-id", w.ID())
	}
	if w.Name() != "test.svc" {
		t.Errorf("Name = %q, want test.svc", w.Name())
	}
	if w.Kind() != "thrift" {
		t.Errorf("Kind = %q, want thrift", w.Kind())
	}
	if w.Addr() != "10.0.0.1:8888" {
		t.Errorf("Addr = %q, want 10.0.0.1:8888", w.Addr())
	}
	if w.Metadata()["key"] != "val" {
		t.Errorf("Metadata[key] = %q, want val", w.Metadata()["key"])
	}
}

func TestResolverAdapterName(t *testing.T) {
	adapter := NewResolverAdapter(nil)
	if adapter.Name() != "beauty-discovery" {
		t.Errorf("Name = %q, want beauty-discovery", adapter.Name())
	}
}

type mockDiscovery struct {
	services []discover.ServiceInfo
	err      error
}

func (m *mockDiscovery) Find(_ context.Context, _ string) ([]discover.ServiceInfo, error) {
	return m.services, m.err
}

func (m *mockDiscovery) Watch(_ context.Context, _ string, _ discover.Notify) error {
	return nil
}

func TestResolverAdapterResolve(t *testing.T) {
	md := &mockDiscovery{
		services: []discover.ServiceInfo{
			{
				Name: "test.svc",
				Addr: "10.0.0.1:8888",
				Metadata: map[string]string{
					"weight": "150",
				},
			},
			{
				Name: "test.svc",
				Addr: "10.0.0.2:8888",
				Metadata: map[string]string{
					"weight": "50",
				},
			},
		},
	}

	adapter := NewResolverAdapter(md)
	result, err := adapter.Resolve(context.Background(), "test.svc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if !result.Cacheable {
		t.Error("result should be cacheable")
	}
	if result.CacheKey != "test.svc" {
		t.Errorf("CacheKey = %q, want test.svc", result.CacheKey)
	}
	if len(result.Instances) != 2 {
		t.Fatalf("Instances count = %d, want 2", len(result.Instances))
	}
	inst := result.Instances[0]
	if inst.Address().String() != "10.0.0.1:8888" {
		t.Errorf("address = %q, want 10.0.0.1:8888", inst.Address().String())
	}
	if inst.Weight() != 150 {
		t.Errorf("weight = %d, want 150", inst.Weight())
	}
}

func TestResolverAdapterResolveDefaultWeight(t *testing.T) {
	md := &mockDiscovery{
		services: []discover.ServiceInfo{
			{Name: "svc", Addr: "10.0.0.1:9999"},
		},
	}

	adapter := NewResolverAdapter(md)
	result, err := adapter.Resolve(context.Background(), "svc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if len(result.Instances) != 1 {
		t.Fatalf("Instances count = %d, want 1", len(result.Instances))
	}
	if result.Instances[0].Weight() != 100 {
		t.Errorf("default weight = %d, want 100", result.Instances[0].Weight())
	}
}
