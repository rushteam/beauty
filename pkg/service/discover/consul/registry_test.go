package consul

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/rushteam/beauty/pkg/service/discover"
)

// fakeAgent 模拟 Consul agent 的注册/TTL/健康查询接口。
type fakeAgent struct {
	mu          sync.Mutex
	services    map[string]bool // 已注册的 service id
	registers   int
	passes      int
	deregisters int

	index   uint64
	entries []*api.ServiceEntry
	changed chan struct{}
}

func newFakeAgent(t *testing.T) (*fakeAgent, *Registry) {
	t.Helper()
	a := &fakeAgent{services: map[string]bool{}, changed: make(chan struct{})}
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	r := NewRegistry(&Config{Addr: u.Host})
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	return a, r
}

func (a *fakeAgent) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p := req.URL.Path
	switch {
	case p == "/v1/agent/service/register":
		var reg api.AgentServiceRegistration
		_ = json.NewDecoder(req.Body).Decode(&reg)
		a.mu.Lock()
		a.services[reg.ID] = true
		a.registers++
		a.mu.Unlock()
	case strings.HasPrefix(p, "/v1/agent/check/update/service:"):
		id := strings.TrimPrefix(p, "/v1/agent/check/update/service:")
		a.mu.Lock()
		ok := a.services[id]
		if ok {
			a.passes++
		}
		a.mu.Unlock()
		if !ok {
			http.Error(w, "Unknown check ID", http.StatusNotFound)
		}
	case strings.HasPrefix(p, "/v1/agent/service/deregister/"):
		a.mu.Lock()
		delete(a.services, strings.TrimPrefix(p, "/v1/agent/service/deregister/"))
		a.deregisters++
		a.mu.Unlock()
	case strings.HasPrefix(p, "/v1/health/service/"):
		want, _ := strconv.ParseUint(req.URL.Query().Get("index"), 10, 64)
		a.mu.Lock()
		idx, ch := a.index, a.changed
		a.mu.Unlock()
		if want != 0 && want == idx {
			select {
			case <-ch:
			case <-time.After(2 * time.Second):
			case <-req.Context().Done():
				return
			}
		}
		a.mu.Lock()
		w.Header().Set("X-Consul-Index", strconv.FormatUint(a.index, 10))
		body, _ := json.Marshal(a.entries)
		a.mu.Unlock()
		_, _ = w.Write(body)
	default:
		http.NotFound(w, req)
	}
}

func (a *fakeAgent) set(index uint64, ids ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.index = index
	a.entries = nil
	for _, id := range ids {
		a.entries = append(a.entries, &api.ServiceEntry{
			Node: &api.Node{Address: "10.0.0.1"},
			Service: &api.AgentService{
				ID: id, Service: "payment", Address: "10.0.0.1", Port: 8080,
				Meta: map[string]string{"kind": "grpc"},
			},
			Checks: api.HealthChecks{{Status: api.HealthPassing}},
		})
	}
	close(a.changed)
	a.changed = make(chan struct{})
}

func (a *fakeAgent) snapshot() (registers, passes, deregisters int, registered bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.registers, a.passes, a.deregisters, a.services["payment-1"]
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

type testService struct{}

func (testService) ID() string                  { return "payment-1" }
func (testService) Name() string                { return "payment" }
func (testService) Kind() string                { return "grpc" }
func (testService) Addr() string                { return "10.0.0.1:8080" }
func (testService) Metadata() map[string]string { return nil }

// agent 丢失本地注册（重启 / 心跳中断超过 DeregisterCriticalServiceAfter）后应自动重新注册；
// 注销后心跳不得再把实例注册回去。
func TestRegister_ReRegistersAfterAgentLosesState(t *testing.T) {
	old := ttlInterval
	ttlInterval = 20 * time.Millisecond
	t.Cleanup(func() { ttlInterval = old })

	a, r := newFakeAgent(t)
	stop, err := r.Register(context.Background(), testService{})
	if err != nil {
		t.Fatal(err)
	}
	if regs, passes, _, _ := a.snapshot(); regs != 1 || passes < 1 {
		t.Fatalf("register should pass TTL immediately: registers=%d passes=%d", regs, passes)
	}

	a.mu.Lock()
	delete(a.services, "payment-1") // 模拟 agent 重启丢状态
	a.mu.Unlock()
	waitUntil(t, "re-register", func() bool {
		regs, _, _, ok := a.snapshot()
		return regs >= 2 && ok
	})

	stop()
	regs, _, deregs, ok := a.snapshot()
	if deregs != 1 || ok {
		t.Fatalf("after stop: deregisters=%d registered=%v", deregs, ok)
	}
	time.Sleep(100 * time.Millisecond)
	if regs2, _, _, ok := a.snapshot(); regs2 != regs || ok {
		t.Fatalf("heartbeat re-registered after stop: registers %d -> %d registered=%v", regs, regs2, ok)
	}
}

// index 回退（集群重建等）后，后续变化仍要能推送出来；初始列表只推一次。
func TestWatch_IndexGoesBackwards(t *testing.T) {
	a, r := newFakeAgent(t)
	a.set(100, "a")

	updates := make(chan []discover.ServiceInfo, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Watch(ctx, "payment", func(ss []discover.ServiceInfo) { updates <- ss }) }()

	next := func() []discover.ServiceInfo {
		t.Helper()
		select {
		case ss := <-updates:
			return ss
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for update")
			return nil
		}
	}
	if got := next(); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("initial = %v", got)
	}
	select {
	case got := <-updates:
		t.Fatalf("duplicate initial notify: %v", got)
	case <-time.After(100 * time.Millisecond):
	}

	a.set(5, "a", "b")
	if got := next(); len(got) != 2 {
		t.Fatalf("after index reset = %v", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Watch did not return after cancel")
	}
}

func TestNextWaitIndex(t *testing.T) {
	cases := []struct{ prev, cur, want uint64 }{
		{0, 10, 10},
		{10, 12, 12},
		{10, 10, 10},
		{10, 3, 0},
		{0, 0, 1},
	}
	for _, c := range cases {
		if got := nextWaitIndex(c.prev, c.cur); got != c.want {
			t.Errorf("nextWaitIndex(%d, %d) = %d, want %d", c.prev, c.cur, got, c.want)
		}
	}
}
