package grpcclient

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

// fakeDiscovery 模拟服务发现后端，支持动态更新地址。
type fakeDiscovery struct {
	mu       sync.Mutex
	services []discover.ServiceInfo
	watchers []discover.Notify
}

func (d *fakeDiscovery) Find(_ context.Context, name string) ([]discover.ServiceInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.services, nil
}

func (d *fakeDiscovery) Watch(ctx context.Context, name string, fn discover.Notify) error {
	d.mu.Lock()
	d.watchers = append(d.watchers, fn)
	fn(d.services)
	d.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (d *fakeDiscovery) update(services []discover.ServiceInfo) {
	d.mu.Lock()
	d.services = services
	watchers := append([]discover.Notify{}, d.watchers...)
	d.mu.Unlock()
	for _, fn := range watchers {
		fn(services)
	}
}

// fakeClientConn captures UpdateState calls from the resolver.
type fakeClientConn struct {
	resolver.ClientConn
	mu     sync.Mutex
	states []resolver.State
	ch     chan struct{}
}

func newFakeCC() *fakeClientConn {
	return &fakeClientConn{ch: make(chan struct{}, 10)}
}

func (cc *fakeClientConn) UpdateState(state resolver.State) error {
	cc.mu.Lock()
	cc.states = append(cc.states, state)
	cc.mu.Unlock()
	select {
	case cc.ch <- struct{}{}:
	default:
	}
	return nil
}

func (cc *fakeClientConn) ReportError(error)                                    {}
func (cc *fakeClientConn) NewAddress([]resolver.Address)                        {}
func (cc *fakeClientConn) ParseServiceConfig(string) *serviceconfig.ParseResult { return nil }

func (cc *fakeClientConn) lastState() resolver.State {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if len(cc.states) == 0 {
		return resolver.State{}
	}
	return cc.states[len(cc.states)-1]
}

func (cc *fakeClientConn) waitUpdate(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-cc.ch:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for resolver update")
	}
}

func TestDiscoveryBuilder_DynamicUpdate(t *testing.T) {
	disc := &fakeDiscovery{
		services: []discover.ServiceInfo{
			{Addr: "10.0.0.1:8080"},
			{Addr: "10.0.0.2:8080"},
		},
	}

	b := &discoveryBuilder{
		registry:    disc,
		serviceName: "test-svc",
	}

	cc := newFakeCC()
	r, err := b.Build(resolver.Target{}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	cc.waitUpdate(t, 2*time.Second)
	state := cc.lastState()
	if len(state.Addresses) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(state.Addresses))
	}

	disc.update([]discover.ServiceInfo{
		{Addr: "10.0.0.3:8080"},
		{Addr: "10.0.0.4:8080"},
		{Addr: "10.0.0.5:8080"},
	})

	cc.waitUpdate(t, 2*time.Second)
	state = cc.lastState()
	if len(state.Addresses) != 3 {
		t.Fatalf("expected 3 addresses after update, got %d", len(state.Addresses))
	}
	addrs := make(map[string]bool)
	for _, a := range state.Addresses {
		addrs[a.Addr] = true
	}
	for _, want := range []string{"10.0.0.3:8080", "10.0.0.4:8080", "10.0.0.5:8080"} {
		if !addrs[want] {
			t.Fatalf("missing addr %s", want)
		}
	}
}

func TestDiscoveryBuilder_WithLabelFilter(t *testing.T) {
	disc := &fakeDiscovery{
		services: []discover.ServiceInfo{
			{Addr: "10.0.0.1:8080", Metadata: map[string]string{"environment": "prod"}},
			{Addr: "10.0.0.2:8080", Metadata: map[string]string{"environment": "staging"}},
			{Addr: "10.0.0.3:8080", Metadata: map[string]string{"environment": "prod"}},
		},
	}

	filter := NewLabelFilter().WithEnvironmentIn("prod")
	b := &discoveryBuilder{
		registry:    disc,
		serviceName: "test-svc",
		labelFilter: filter,
	}

	cc := newFakeCC()
	r, err := b.Build(resolver.Target{}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	cc.waitUpdate(t, 2*time.Second)
	state := cc.lastState()
	if len(state.Addresses) != 2 {
		t.Fatalf("expected 2 prod addresses, got %d", len(state.Addresses))
	}
}

func TestDiscoveryBuilder_Close(t *testing.T) {
	disc := &fakeDiscovery{
		services: []discover.ServiceInfo{{Addr: "10.0.0.1:8080"}},
	}

	b := &discoveryBuilder{
		registry:    disc,
		serviceName: "test-svc",
	}

	cc := newFakeCC()
	r, err := b.Build(resolver.Target{}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}

	cc.waitUpdate(t, 2*time.Second)
	r.Close()
	time.Sleep(50 * time.Millisecond)
}
