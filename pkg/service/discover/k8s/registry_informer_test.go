package k8s

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func testService(ns, name string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
}

func testSlice(ns, svc, name string, port int32, addrs ...string) *discoveryv1.EndpointSlice {
	eps := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns, Name: name,
			Labels: map[string]string{discoveryv1.LabelServiceName: svc},
		},
		Ports: []discoveryv1.EndpointPort{{Port: &port}},
	}
	for _, a := range addrs {
		eps.Endpoints = append(eps.Endpoints, discoveryv1.Endpoint{Addresses: []string{a}})
	}
	return eps
}

type countingClient struct {
	*fake.Clientset
	lists   atomic.Int64
	watches atomic.Int64
}

func newCountingRegistry(ns string, objs ...runtime.Object) (*Registry, *countingClient) {
	c := &countingClient{Clientset: fake.NewSimpleClientset(objs...)}
	c.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		c.lists.Add(1)
		return false, nil, nil
	})
	c.PrependWatchReactor("*", func(k8stesting.Action) (bool, watch.Interface, error) {
		c.watches.Add(1)
		return false, nil, nil
	})
	return &Registry{
		config:   &Config{Namespace: ns, WatchTimeout: 30},
		client:   c.Clientset,
		watchers: make(map[uint64]context.CancelFunc),
	}, c
}

func addrs(ss []discover.ServiceInfo) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Addr)
	}
	slices.Sort(out)
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
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

// 事件只触发本地缓存刷新：同步之后的变更不再访问 API server，且无变化时不通知。
func TestWatch_InformerRefreshFromCache(t *testing.T) {
	r, c := newCountingRegistry("default",
		testService("default", "payment"),
		testSlice("default", "payment", "payment-a", 8080, "10.0.0.1"),
	)

	updates := make(chan []discover.ServiceInfo, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- r.Watch(ctx, "payment", func(ss []discover.ServiceInfo) { updates <- ss })
	}()

	next := func() []discover.ServiceInfo {
		t.Helper()
		select {
		case ss := <-updates:
			return ss
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for notify")
			return nil
		}
	}

	if got := addrs(next()); !slices.Equal(got, []string{"10.0.0.1:8080"}) {
		t.Fatalf("initial = %v", got)
	}
	// fake clientset 不支持 ResourceVersion 续订，须等 Watch 建立后再改对象，否则事件会丢
	waitFor(t, "both watches established", func() bool { return c.watches.Load() >= 2 })
	time.Sleep(50 * time.Millisecond)
	listsAfterSync := c.lists.Load()

	slicesAPI := c.DiscoveryV1().EndpointSlices("default")
	if _, err := slicesAPI.Update(ctx, testSlice("default", "payment", "payment-a", 8080, "10.0.0.1", "10.0.0.2"), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := addrs(next()); !slices.Equal(got, []string{"10.0.0.1:8080", "10.0.0.2:8080"}) {
		t.Fatalf("after scale up = %v", got)
	}

	// 只改与端点无关的字段：不应重复通知
	noop := testSlice("default", "payment", "payment-a", 8080, "10.0.0.1", "10.0.0.2")
	noop.Annotations = map[string]string{"touched": "1"}
	if _, err := slicesAPI.Update(ctx, noop, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := slicesAPI.Create(ctx, testSlice("default", "payment", "payment-b", 8080, "10.0.0.3"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := addrs(next()); !slices.Equal(got, []string{"10.0.0.1:8080", "10.0.0.2:8080", "10.0.0.3:8080"}) {
		t.Fatalf("no-op update must not notify; next = %v", got)
	}

	if n := c.lists.Load(); n != listsAfterSync {
		t.Fatalf("refresh hit API server: lists %d -> %d", listsAfterSync, n)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Watch did not return after cancel")
	}
}

// 通配查找固定两次 List，且跨 namespace 的同名 Service 不串端点。
func TestFind_WildcardTwoListsAndNamespaceIsolation(t *testing.T) {
	r, c := newCountingRegistry("",
		testService("a", "api"),
		testService("b", "api"),
		testService("a", "web"),
		testSlice("a", "api", "api-1", 80, "10.0.1.1"),
		testSlice("b", "api", "api-1", 80, "10.0.2.1"),
		testSlice("a", "web", "web-1", 80, "10.0.3.1", "10.0.3.2"),
	)

	got, err := r.Find(context.Background(), "*")
	if err != nil {
		t.Fatal(err)
	}
	if n := c.lists.Load(); n != 2 {
		t.Fatalf("Find issued %d lists, want 2", n)
	}

	byNS := map[string][]string{}
	for _, s := range got {
		byNS[s.Metadata["namespace"]+"/"+s.Name] = append(byNS[s.Metadata["namespace"]+"/"+s.Name], s.Addr)
	}
	want := map[string][]string{
		"a/api": {"10.0.1.1:80"},
		"b/api": {"10.0.2.1:80"},
		"a/web": {"10.0.3.1:80", "10.0.3.2:80"},
	}
	for k, w := range want {
		g := byNS[k]
		slices.Sort(g)
		if !slices.Equal(g, w) {
			t.Errorf("%s = %v, want %v", k, g, w)
		}
	}
	if len(byNS) != len(want) {
		t.Errorf("unexpected groups: %v", byNS)
	}
}
