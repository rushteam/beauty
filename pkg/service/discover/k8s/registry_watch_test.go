package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"k8s.io/client-go/kubernetes/fake"
)

func newFakeRegistry() *Registry {
	return &Registry{
		config:   &Config{Namespace: "default", WatchTimeout: 30},
		client:   fake.NewSimpleClientset(),
		watchers: make(map[uint64]context.CancelFunc),
	}
}

func startWatch(r *Registry, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- r.Watch(ctx, "payment", func([]discover.ServiceInfo) {}) }()
	return done
}

func waitWatchers(t *testing.T, r *Registry, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.watcherMu.RLock()
		n := len(r.watchers)
		r.watcherMu.RUnlock()
		if n == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("watchers count did not reach %d", want)
}

// 同一 Registry 上多个调用方 Watch 同一服务时不能互相取消。
func TestWatch_SameServiceDoesNotCancelEachOther(t *testing.T) {
	r := newFakeRegistry()

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	doneA := startWatch(r, ctxA)
	waitWatchers(t, r, 1)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	doneB := startWatch(r, ctxB)
	waitWatchers(t, r, 2)

	select {
	case err := <-doneA:
		t.Fatalf("watch A returned after B started (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	// 取消 A 只影响 A，B 继续运行且仍可被 Close 取消
	cancelA()
	select {
	case <-doneA:
	case <-time.After(2 * time.Second):
		t.Fatal("watch A did not return after its ctx was canceled")
	}
	waitWatchers(t, r, 1)
	select {
	case err := <-doneB:
		t.Fatalf("watch B returned after A was canceled (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-doneB:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not stop watch B")
	}
}
