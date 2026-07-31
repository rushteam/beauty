package cache_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/middleware/cache"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// fakeInvoker returns a grpc.UnaryInvoker that populates reply with value
// and counts invocations.
func fakeInvoker(value string, calls *atomic.Int64) grpc.UnaryInvoker {
	return func(_ context.Context, _ string, _, reply any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls.Add(1)
		reply.(*wrapperspb.StringValue).Value = value
		return nil
	}
}

const testMethod = "/test.Svc/GetItem"

// 第二次调用命中缓存,后端只调用一次。
func TestInterceptor_HitMiss(t *testing.T) {
	var calls atomic.Int64
	invoker := fakeInvoker("hello", &calls)
	interceptor := cache.UnaryClientInterceptor(cache.NewMemoryStore(16))

	req := wrapperspb.String("world")

	reply1 := &wrapperspb.StringValue{}
	if err := interceptor(context.Background(), testMethod, req, reply1, nil, invoker); err != nil {
		t.Fatal(err)
	}

	reply2 := &wrapperspb.StringValue{}
	if err := interceptor(context.Background(), testMethod, req, reply2, nil, invoker); err != nil {
		t.Fatal(err)
	}

	if reply1.Value != "hello" || reply2.Value != "hello" {
		t.Fatalf("reply = %q / %q, want hello", reply1.Value, reply2.Value)
	}
	if calls.Load() != 1 {
		t.Fatalf("后端调用 %d 次, want 1", calls.Load())
	}
}

// 不同请求不共享缓存。
func TestInterceptor_DifferentRequestsDifferentEntries(t *testing.T) {
	var calls atomic.Int64
	invoker := func(_ context.Context, _ string, req, reply any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls.Add(1)
		reply.(*wrapperspb.StringValue).Value = "echo-" + req.(*wrapperspb.StringValue).Value
		return nil
	}
	interceptor := cache.UnaryClientInterceptor(cache.NewMemoryStore(16))

	r1 := &wrapperspb.StringValue{}
	interceptor(context.Background(), testMethod, wrapperspb.String("a"), r1, nil, invoker)
	r2 := &wrapperspb.StringValue{}
	interceptor(context.Background(), testMethod, wrapperspb.String("b"), r2, nil, invoker)

	if r1.Value != "echo-a" || r2.Value != "echo-b" {
		t.Fatalf("got %q / %q, want echo-a / echo-b", r1.Value, r2.Value)
	}
	if calls.Load() != 2 {
		t.Fatalf("不同请求应各调用一次, got %d", calls.Load())
	}
}

// WithMethods 限制只缓存指定方法。
func TestInterceptor_MethodFilter(t *testing.T) {
	var calls atomic.Int64
	invoker := fakeInvoker("v", &calls)
	interceptor := cache.UnaryClientInterceptor(
		cache.NewMemoryStore(16),
		cache.WithMethods("/test.Svc/Cached"),
	)

	req := wrapperspb.String("x")

	// 不在白名单的方法不缓存
	interceptor(context.Background(), "/test.Svc/NotCached", req, &wrapperspb.StringValue{}, nil, invoker)
	interceptor(context.Background(), "/test.Svc/NotCached", req, &wrapperspb.StringValue{}, nil, invoker)
	if calls.Load() != 2 {
		t.Fatalf("非白名单方法不应缓存, got %d calls", calls.Load())
	}

	calls.Store(0)
	// 白名单方法缓存
	interceptor(context.Background(), "/test.Svc/Cached", req, &wrapperspb.StringValue{}, nil, invoker)
	interceptor(context.Background(), "/test.Svc/Cached", req, &wrapperspb.StringValue{}, nil, invoker)
	if calls.Load() != 1 {
		t.Fatalf("白名单方法应缓存, got %d calls", calls.Load())
	}
}

// WithFilter 自定义过滤。
func TestInterceptor_CustomFilter(t *testing.T) {
	var calls atomic.Int64
	invoker := fakeInvoker("v", &calls)
	interceptor := cache.UnaryClientInterceptor(
		cache.NewMemoryStore(16),
		cache.WithFilter(func(_ context.Context, _ string, req any) bool {
			return req.(*wrapperspb.StringValue).Value != "skip"
		}),
	)

	interceptor(context.Background(), testMethod, wrapperspb.String("skip"), &wrapperspb.StringValue{}, nil, invoker)
	interceptor(context.Background(), testMethod, wrapperspb.String("skip"), &wrapperspb.StringValue{}, nil, invoker)
	if calls.Load() != 2 {
		t.Fatalf("filter=false 不应缓存, got %d", calls.Load())
	}
}

// TTL 过期后重新调用后端。
func TestInterceptor_TTLExpiry(t *testing.T) {
	var calls atomic.Int64
	invoker := fakeInvoker("v", &calls)
	interceptor := cache.UnaryClientInterceptor(
		cache.NewMemoryStore(16),
		cache.WithDefaultTTL(20*time.Millisecond),
	)

	req := wrapperspb.String("x")
	interceptor(context.Background(), testMethod, req, &wrapperspb.StringValue{}, nil, invoker)
	time.Sleep(30 * time.Millisecond)
	interceptor(context.Background(), testMethod, req, &wrapperspb.StringValue{}, nil, invoker)

	if calls.Load() != 2 {
		t.Fatalf("TTL 过期后应重新调用, got %d", calls.Load())
	}
}

// RPC 返回错误时不缓存。
func TestInterceptor_ErrorNotCached(t *testing.T) {
	var calls atomic.Int64
	errInvoker := func(_ context.Context, _ string, _, reply any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls.Add(1)
		return context.DeadlineExceeded
	}
	interceptor := cache.UnaryClientInterceptor(cache.NewMemoryStore(16))
	req := wrapperspb.String("x")

	interceptor(context.Background(), testMethod, req, &wrapperspb.StringValue{}, nil, errInvoker)
	interceptor(context.Background(), testMethod, req, &wrapperspb.StringValue{}, nil, errInvoker)

	if calls.Load() != 2 {
		t.Fatalf("错误响应不应缓存, got %d", calls.Load())
	}
}

// Stats 返回正确的命中/未命中计数。
func TestInterceptor_Stats(t *testing.T) {
	var calls atomic.Int64
	invoker := fakeInvoker("v", &calls)
	ci := cache.NewCacheInterceptor(cache.NewMemoryStore(16))
	interceptor := ci.UnaryClientInterceptor()

	req := wrapperspb.String("x")
	interceptor(context.Background(), testMethod, req, &wrapperspb.StringValue{}, nil, invoker)
	interceptor(context.Background(), testMethod, req, &wrapperspb.StringValue{}, nil, invoker)
	interceptor(context.Background(), testMethod, req, &wrapperspb.StringValue{}, nil, invoker)

	s := ci.Stats()
	if s.Hits != 2 || s.Misses != 1 {
		t.Fatalf("stats = %+v, want Hits=2 Misses=1", s)
	}
}

// Singleflight:并发调用同方法同参数只打一次后端。
func TestInterceptor_SingleFlight(t *testing.T) {
	var calls atomic.Int64
	slowInvoker := func(_ context.Context, _ string, _, reply any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		reply.(*wrapperspb.StringValue).Value = "shared"
		return nil
	}

	interceptor := cache.UnaryClientInterceptor(
		cache.NewMemoryStore(16),
		cache.WithSingleFlight(),
	)

	const n = 10
	req := wrapperspb.String("x")
	var wg sync.WaitGroup
	replies := make([]*wrapperspb.StringValue, n)
	for i := range n {
		replies[i] = &wrapperspb.StringValue{}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			interceptor(context.Background(), testMethod, req, replies[idx], nil, slowInvoker)
		}(i)
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("singleflight 下后端应只调用 1 次, got %d", calls.Load())
	}
	for i, r := range replies {
		if r.Value != "shared" {
			t.Fatalf("goroutine %d reply = %q, want shared", i, r.Value)
		}
	}
}

// 自定义 key 函数。
func TestInterceptor_CustomKeyFunc(t *testing.T) {
	var calls atomic.Int64
	invoker := fakeInvoker("v", &calls)
	interceptor := cache.UnaryClientInterceptor(
		cache.NewMemoryStore(16),
		cache.WithKeyFunc(func(method string, req any) string {
			return "static"
		}),
	)

	interceptor(context.Background(), "/a/B", wrapperspb.String("1"), &wrapperspb.StringValue{}, nil, invoker)
	interceptor(context.Background(), "/c/D", wrapperspb.String("2"), &wrapperspb.StringValue{}, nil, invoker)

	if calls.Load() != 1 {
		t.Fatalf("相同 key 应命中, got %d", calls.Load())
	}
}

// MemoryStore 容量满时淘汰。
func TestMemoryStore_Eviction(t *testing.T) {
	m := cache.NewMemoryStore(2)
	m.Set("a", []byte("1"), time.Minute)
	m.Set("b", []byte("2"), time.Minute)
	m.Set("c", []byte("3"), time.Minute)
	if m.Len() != 2 {
		t.Fatalf("len = %d, want 2", m.Len())
	}
}

// MemoryStore TTL 过期。
func TestMemoryStore_TTLExpiry(t *testing.T) {
	m := cache.NewMemoryStore(16)
	m.Set("k", []byte("v"), 10*time.Millisecond)
	if _, ok := m.Get("k"); !ok {
		t.Fatal("should hit before expiry")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := m.Get("k"); ok {
		t.Fatal("should miss after expiry")
	}
}

// MemoryStore Delete。
func TestMemoryStore_Delete(t *testing.T) {
	m := cache.NewMemoryStore(16)
	m.Set("x", []byte("v"), time.Minute)
	m.Delete("x")
	if _, ok := m.Get("x"); ok {
		t.Fatal("should be deleted")
	}
}
