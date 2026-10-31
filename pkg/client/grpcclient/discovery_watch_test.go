package grpcclient

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
)

// reconnectDiscovery 首次 Watch 立即返回（模拟断连），之后推送 updated 并阻塞到 ctx 取消。
type reconnectDiscovery struct {
	calls            atomic.Int32
	initial, updated []discover.ServiceInfo
}

func (d *reconnectDiscovery) Find(context.Context, string) ([]discover.ServiceInfo, error) {
	return d.initial, nil
}

func (d *reconnectDiscovery) Watch(ctx context.Context, _ string, n discover.Notify) error {
	if d.calls.Add(1) == 1 {
		return errors.New("disconnected")
	}
	n(d.updated)
	<-ctx.Done()
	return ctx.Err()
}

// 有连接在排空期时，Stop 不能被 drainTimeout 拖住。
func TestServiceDiscoveryClient_StopDoesNotWaitDrain(t *testing.T) {
	d := &reconnectDiscovery{
		initial: []discover.ServiceInfo{{Addr: "10.0.0.1:80"}},
		updated: []discover.ServiceInfo{{Addr: "10.0.0.2:80"}},
	}
	c := NewServiceDiscoveryClient(d, "svc",
		WithDiscoveryInsecure(), WithDiscoveryHealthCheck(false, 0), WithDiscoveryDrainTimeout(time.Hour))
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.GetClient(t.Context()); err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for s := c.GetServiceInfo(); len(s) != 1 || s[0].Addr != "10.0.0.2:80"; s = c.GetServiceInfo() {
		if time.Now().After(deadline) {
			t.Fatalf("removed node not applied, services=%v", s)
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Close blocked %v waiting for drain", d)
	}
}

func TestServiceDiscoveryClient_ResubscribesAfterWatchReturns(t *testing.T) {
	d := &reconnectDiscovery{
		initial: []discover.ServiceInfo{{Addr: "10.0.0.1:80"}},
		updated: []discover.ServiceInfo{{Addr: "10.0.0.1:80"}, {Addr: "10.0.0.2:80"}},
	}
	c := NewServiceDiscoveryClient(d, "svc", WithDiscoveryHealthCheck(false, 0))
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	deadline := time.Now().Add(3 * time.Second)
	for len(c.GetServiceInfo()) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("service list not updated after resubscribe, watch calls=%d", d.calls.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
