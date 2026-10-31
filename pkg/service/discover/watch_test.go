package discover

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// flakyDiscovery 前 failN 次 Watch 立即返回（模拟断连/被取消），之后阻塞到 ctx 取消。
type flakyDiscovery struct {
	failN int32
	calls atomic.Int32
}

func (d *flakyDiscovery) Find(context.Context, string) ([]ServiceInfo, error) { return nil, nil }

func (d *flakyDiscovery) Watch(ctx context.Context, _ string, n Notify) error {
	if d.calls.Add(1) <= d.failN {
		return errors.New("disconnected")
	}
	n([]ServiceInfo{{Addr: "10.0.0.1:80"}})
	<-ctx.Done()
	return ctx.Err()
}

func shortRetry(t *testing.T) {
	t.Helper()
	oldBase, oldMax := watchRetryBase, watchRetryMax
	watchRetryBase, watchRetryMax = 5*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { watchRetryBase, watchRetryMax = oldBase, oldMax })
}

func TestWatchLoop_ResubscribesAfterEarlyReturn(t *testing.T) {
	shortRetry(t)
	d := &flakyDiscovery{failN: 3}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notified := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchLoop(ctx, d, "svc", func([]ServiceInfo) {
			select {
			case notified <- struct{}{}:
			default:
			}
		})
	}()

	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected notify after resubscribe, watch calls=%d", d.calls.Load())
	}
	if got := d.calls.Load(); got != 4 {
		t.Fatalf("watch calls = %d, want 4 (3 early returns + 1 healthy)", got)
	}

	// 健康的 Watch 阻塞期间不应再被重复订阅
	time.Sleep(100 * time.Millisecond)
	if got := d.calls.Load(); got != 4 {
		t.Fatalf("healthy watch must not be resubscribed, calls = %d", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WatchLoop did not return after ctx cancel")
	}
}

// scriptedDiscovery 每次 Watch 依次推送 pushes[i]，然后立即返回触发重订阅；脚本用完后阻塞。
type scriptedDiscovery struct {
	pushes [][][]ServiceInfo
	calls  atomic.Int32
}

func (d *scriptedDiscovery) Find(context.Context, string) ([]ServiceInfo, error) { return nil, nil }

func (d *scriptedDiscovery) Watch(ctx context.Context, _ string, n Notify) error {
	i := int(d.calls.Add(1)) - 1
	if i >= len(d.pushes) {
		<-ctx.Done()
		return ctx.Err()
	}
	for _, p := range d.pushes[i] {
		n(p)
	}
	return errors.New("disconnected")
}

func TestWatchLoop_EmptyPushProtection(t *testing.T) {
	shortRetry(t)
	a := []ServiceInfo{{Addr: "10.0.0.1:80"}}
	b := []ServiceInfo{{Addr: "10.0.0.2:80"}}
	d := &scriptedDiscovery{pushes: [][][]ServiceInfo{
		{nil, a, {}}, // 首个空推送照常下发；有实例后的空推送被拦截
		{nil, b},     // 重订阅后的首次空推送同样被拦截，随后的非空推送正常下发
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer func() {
		cancel()
		<-done
	}()

	var got [][]ServiceInfo
	gotCh := make(chan []ServiceInfo, 16)
	go func() {
		defer close(done)
		WatchLoop(ctx, d, "svc", func(s []ServiceInfo) { gotCh <- s })
	}()
	for len(got) < 3 {
		select {
		case s := <-gotCh:
			got = append(got, s)
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout, got %v", got)
		}
	}
	select {
	case s := <-gotCh:
		t.Fatalf("unexpected extra push %v (all: %v)", s, got)
	case <-time.After(50 * time.Millisecond):
	}
	if len(got[0]) != 0 || got[1][0].Addr != a[0].Addr || got[2][0].Addr != b[0].Addr {
		t.Fatalf("pushes = %v, want [] -> a -> b", got)
	}
}

func TestWatchLoop_StopsDuringBackoff(t *testing.T) {
	oldBase, oldMax := watchRetryBase, watchRetryMax
	watchRetryBase, watchRetryMax = time.Hour, time.Hour
	t.Cleanup(func() { watchRetryBase, watchRetryMax = oldBase, oldMax })

	d := &flakyDiscovery{failN: 1 << 30}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchLoop(ctx, d, "svc", func([]ServiceInfo) {})
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WatchLoop should return promptly when ctx is canceled during backoff")
	}
	if got := d.calls.Load(); got != 1 {
		t.Fatalf("watch calls = %d, want 1", got)
	}
}
