package resty_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	httpclient "github.com/rushteam/beauty/pkg/client/http"
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

func TestWatch_ResubscribesAfterWatchReturns(t *testing.T) {
	d := &reconnectDiscovery{
		initial: []discover.ServiceInfo{newService("10.0.0.1:80", 1, nil)},
		updated: []discover.ServiceInfo{newService("10.0.0.1:80", 1, nil), newService("10.0.0.2:80", 1, nil)},
	}
	cli := httpclient.NewServiceDiscoveryHTTPClient(d, "test-svc")
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cli.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for len(cli.GetServiceInfo()) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("service list not updated after resubscribe, watch calls=%d", d.calls.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
