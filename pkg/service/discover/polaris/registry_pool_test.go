package polaris

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/rushteam/beauty/pkg/service/discover/internal/clientpool"
)

type fakeConsumer struct {
	api.ConsumerAPI
	calls atomic.Int32
}

func (f *fakeConsumer) GetInstances(*api.GetInstancesRequest) (*model.InstancesResponse, error) {
	f.calls.Add(1)
	return &model.InstancesResponse{}, nil
}

func TestFind_SharesAndReleasesConsumer(t *testing.T) {
	var created, destroyed atomic.Int32
	fc := &fakeConsumer{}
	oldNew, oldPool := newConsumerClient, consumers
	newConsumerClient = func(*Config) (*consumerClient, error) {
		created.Add(1)
		return &consumerClient{api: fc, destroy: func() { destroyed.Add(1) }}, nil
	}
	consumers = clientpool.New(20*time.Millisecond, func(c *consumerClient) { c.destroy() })
	t.Cleanup(func() { newConsumerClient, consumers = oldNew, oldPool })

	// 模拟 resolver 每次 Build 都 NewFromURL；hold 保持一个引用，避免循环间隙触发闲置关闭
	u, _ := url.Parse("polaris://127.0.0.1:8091/default")
	holder, _ := NewFromURL(*u)
	_, hold, err := holder.acquireConsumer()
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		reg, err := NewFromURL(*u)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reg.Find(context.Background(), "svc"); err != nil {
			t.Fatal(err)
		}
	}
	hold()
	if got := created.Load(); got != 1 {
		t.Fatalf("created %d SDK contexts, want 1 shared", got)
	}
	if fc.calls.Load() != 3 {
		t.Fatalf("GetInstances calls = %d, want 3", fc.calls.Load())
	}

	deadline := time.Now().Add(time.Second)
	for destroyed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("SDK context not destroyed after idle timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := destroyed.Load(); got != 1 {
		t.Fatalf("destroyed %d times, want 1", got)
	}
}
