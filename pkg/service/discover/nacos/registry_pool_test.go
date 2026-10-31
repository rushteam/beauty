package nacos

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/rushteam/beauty/pkg/infra/nacos"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/discover/internal/clientpool"
)

type fakeNaming struct {
	naming_client.INamingClient
	subs, unsubs atomic.Int32
	closed       atomic.Int32
}

func (f *fakeNaming) SelectInstances(vo.SelectInstancesParam) ([]model.Instance, error) {
	return nil, nil
}
func (f *fakeNaming) Subscribe(*vo.SubscribeParam) error   { f.subs.Add(1); return nil }
func (f *fakeNaming) Unsubscribe(*vo.SubscribeParam) error { f.unsubs.Add(1); return nil }
func (f *fakeNaming) CloseClient()                         { f.closed.Add(1) }

// useFakeNaming 替换客户端构造与共享池，返回已创建的 fake 列表。
func useFakeNaming(t *testing.T, idle time.Duration) func() []*fakeNaming {
	t.Helper()
	var mu sync.Mutex
	var created []*fakeNaming
	oldNew, oldPool := newNamingClient, discoveryClients
	newNamingClient = func(*nacos.Config) (naming_client.INamingClient, error) {
		f := &fakeNaming{}
		mu.Lock()
		created = append(created, f)
		mu.Unlock()
		return f, nil
	}
	discoveryClients = clientpool.New(idle, func(c naming_client.INamingClient) { c.CloseClient() })
	t.Cleanup(func() { newNamingClient, discoveryClients = oldNew, oldPool })
	return func() []*fakeNaming {
		mu.Lock()
		defer mu.Unlock()
		return append([]*fakeNaming(nil), created...)
	}
}

// 模拟 resolver 每次 Build 都 NewFromURL：同一配置只应创建一个 client，
// 所有 Watch 结束且闲置超时后 client 必须被关闭。
func TestWatch_SharesAndReleasesDiscoveryClient(t *testing.T) {
	created := useFakeNaming(t, 30*time.Millisecond)
	u, _ := url.Parse("nacos://127.0.0.1:8848/svc?namespace=dev")

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range 5 {
		reg, err := NewFromURL(*u)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = reg.Watch(ctx, "svc", func([]discover.ServiceInfo) {})
		}()
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if cs := created(); len(cs) == 1 && cs[0].subs.Load() == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want 1 shared client with 5 subscriptions, got %d clients", len(created()))
		}
		time.Sleep(5 * time.Millisecond)
	}
	f := created()[0]

	cancel()
	wg.Wait()
	if got := f.unsubs.Load(); got != 5 {
		t.Fatalf("unsubscribes = %d, want 5", got)
	}
	deadline = time.Now().Add(time.Second)
	for f.closed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("client not closed after all watches ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.closed.Load(); got != 1 {
		t.Fatalf("CloseClient called %d times, want 1", got)
	}
}

func TestFind_ReleasesDiscoveryClient(t *testing.T) {
	created := useFakeNaming(t, 10*time.Millisecond)
	reg := NewRegistry(&Config{Addr: []string{"127.0.0.1:8848"}})
	// hold 保持一个引用，避免循环间隙触发闲置关闭
	_, hold, err := reg.acquireDiscoveryClient()
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := reg.Find(context.Background(), "svc"); err != nil {
			t.Fatal(err)
		}
	}
	hold()
	if n := len(created()); n != 1 {
		t.Fatalf("repeated Find created %d clients, want 1", n)
	}
	deadline := time.Now().Add(time.Second)
	for created()[0].closed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("client not closed after Find finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
