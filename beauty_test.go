package beauty

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
)

// --- stub service helpers ---

// stubService 是最简单的 Service stub：Start 阻塞直到 ctx 取消，退出前记录时间戳。
type stubService struct {
	name    string
	stopped atomic.Int64 // UnixNano，Stop() 时间戳
}

func (s *stubService) Start(ctx context.Context) error {
	<-ctx.Done()
	// 模拟实际服务需要一点时间完成 graceful drain
	time.Sleep(10 * time.Millisecond)
	s.stopped.Store(time.Now().UnixNano())
	return nil
}

func (s *stubService) String() string { return s.name }

// slowService Start 阻塞 duration 后才退出，用于验证多服务并发等待。
type slowService struct {
	name     string
	duration time.Duration
	stopped  atomic.Int64
}

func (s *slowService) Start(ctx context.Context) error {
	<-ctx.Done()
	time.Sleep(s.duration)
	s.stopped.Store(time.Now().UnixNano())
	return nil
}
func (s *slowService) String() string { return s.name }

// readyService 实现 ReadyNotifier，在 Start 内部延迟 delay 后发出就绪信号。
type readyService struct {
	name      string
	readyCh   chan struct{}
	readyOnce sync.Once
	registered atomic.Bool
}

func newReadyService(name string) *readyService {
	return &readyService{name: name, readyCh: make(chan struct{})}
}

func (s *readyService) Start(ctx context.Context) error {
	// 延迟 20ms 后才就绪
	time.AfterFunc(20*time.Millisecond, func() {
		s.readyOnce.Do(func() { close(s.readyCh) })
	})
	<-ctx.Done()
	return nil
}
func (s *readyService) Ready() <-chan struct{} { return s.readyCh }
func (s *readyService) String() string        { return s.name }

// stubRegistry 记录注册/注销顺序，实现 discover.Registry 接口。
type stubRegistry struct {
	mu          sync.Mutex
	registered  []string
	registered2 atomic.Bool
}

func (r *stubRegistry) Register(_ context.Context, svc discover.Service) (context.CancelFunc, error) {
	r.mu.Lock()
	r.registered = append(r.registered, svc.Name())
	r.mu.Unlock()
	r.registered2.Store(true)
	return func() {}, nil
}

// --- tests ---

// TestGracefulShutdown_HookAfterServiceStop 验证核心修复：
// AfterRun hook 的执行时间必须晚于所有服务 Start() 返回的时间。
func TestGracefulShutdown_HookAfterServiceStop(t *testing.T) {
	svc := &stubService{name: "svc"}
	var hookCalledAt int64

	ctx, cancel := context.WithCancel(context.Background())

	app := New(WithService(svc))
	app.Hook(EventAfterRun, func(_ *App) {
		atomic.StoreInt64(&hookCalledAt, time.Now().UnixNano())
	})

	done := make(chan error, 1)
	go func() { done <- app.Start(ctx) }()

	// 等 app 进入 ready 状态
	waitReady(t, app)

	// 触发 shutdown
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("app.Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("app.Start did not return within 2s")
	}

	svcStoppedAt := svc.stopped.Load()
	if svcStoppedAt == 0 {
		t.Fatal("service never recorded its stop time")
	}
	if hookCalledAt == 0 {
		t.Fatal("AfterRun hook was never called")
	}
	if hookCalledAt < svcStoppedAt {
		t.Errorf("AfterRun hook ran BEFORE service stopped: hook=%d svc=%d diff=%dµs",
			hookCalledAt, svcStoppedAt, (svcStoppedAt-hookCalledAt)/1000)
	}
}

// TestGracefulShutdown_MultiService 验证多个服务都完成后才执行 hook。
func TestGracefulShutdown_MultiService(t *testing.T) {
	svc1 := &slowService{name: "svc1", duration: 20 * time.Millisecond}
	svc2 := &slowService{name: "svc2", duration: 40 * time.Millisecond}
	var hookCalledAt int64

	ctx, cancel := context.WithCancel(context.Background())
	app := New(WithService(svc1), WithService(svc2))
	app.Hook(EventAfterRun, func(_ *App) {
		atomic.StoreInt64(&hookCalledAt, time.Now().UnixNano())
	})

	done := make(chan error, 1)
	go func() { done <- app.Start(ctx) }()

	waitReady(t, app)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("app.Start did not return within 3s")
	}

	hook := atomic.LoadInt64(&hookCalledAt)
	for _, svc := range []*slowService{svc1, svc2} {
		stoppedAt := svc.stopped.Load()
		if stoppedAt == 0 {
			t.Errorf("%s: stop time never recorded", svc.name)
			continue
		}
		if hook < stoppedAt {
			t.Errorf("AfterRun hook ran before %s stopped: hook=%d stopped=%d",
				svc.name, hook, stoppedAt)
		}
	}
}

// TestGracefulShutdown_BeforeRunHook 验证 BeforeRun hook 在服务启动前执行。
func TestGracefulShutdown_BeforeRunHook(t *testing.T) {
	var hookCalledAt int64
	var svcStartedAt int64

	ctx, cancel := context.WithCancel(context.Background())

	svc := &recordStartService{
		startedAt: &svcStartedAt,
		delay:     5 * time.Millisecond,
	}

	app := New(WithService(svc))
	app.Hook(EventBeforeRun, func(_ *App) {
		atomic.StoreInt64(&hookCalledAt, time.Now().UnixNano())
	})

	done := make(chan error, 1)
	go func() { done <- app.Start(ctx) }()

	waitReady(t, app)
	cancel()
	<-done

	if hookCalledAt == 0 {
		t.Fatal("BeforeRun hook was never called")
	}
	started := atomic.LoadInt64(&svcStartedAt)
	if started != 0 && hookCalledAt > started {
		t.Errorf("BeforeRun hook ran AFTER service started: hook=%d started=%d",
			hookCalledAt, started)
	}
}

// TestGracefulShutdown_ReadyNotifier 验证 ReadyNotifier 未发出信号前不触发注册。
func TestGracefulShutdown_ReadyNotifier(t *testing.T) {
	svc := newReadyService("ready-svc")
	reg := &stubRegistry{}

	// 为了让 registry 被触发，需要将 svc 同时实现 discover.Service。
	// 这里用 wrappedDiscoverService 包装。
	wrapped := &wrappedDiscoverService{readyService: svc, name: "ready-svc"}

	ctx, cancel := context.WithCancel(context.Background())
	app := &App{hooks: make(map[HookEvent][]HookFunc)}
	app.services = append(app.services, wrapped)
	app.registry = append(app.registry, reg)

	done := make(chan error, 1)
	go func() { done <- app.Start(ctx) }()

	// ready 信号发出前，注册不应发生
	time.Sleep(5 * time.Millisecond)
	if reg.registered2.Load() {
		t.Error("registry.Register called before service was ready")
	}

	// 等 ready 信号发出（~20ms）
	select {
	case <-svc.readyCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("service never became ready")
	}
	time.Sleep(5 * time.Millisecond) // 给注册 goroutine 时间执行

	if !reg.registered2.Load() {
		t.Error("registry.Register not called after service became ready")
	}

	cancel()
	<-done
}

// TestGracefulShutdown_ServiceError 验证服务启动失败时 app 能正常退出。
func TestGracefulShutdown_ServiceError(t *testing.T) {
	svc := &errorService{name: "err-svc"}
	ctx := context.Background()

	app := New(WithService(svc))
	done := make(chan error, 1)
	go func() { done <- app.Start(ctx) }()

	select {
	case err := <-done:
		// 服务报错后 app 应退出，Start 本身返回 nil（错误已被 logger 记录）
		if err != nil {
			t.Fatalf("unexpected error from app.Start: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("app.Start did not return after service error")
	}
}

// --- helpers ---

func waitReady(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.Ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("app never became ready")
}

// recordStartService 记录 Start 被调用的时间。
type recordStartService struct {
	name      string
	startedAt *int64
	delay     time.Duration
}

func (s *recordStartService) Start(ctx context.Context) error {
	time.Sleep(s.delay)
	atomic.StoreInt64(s.startedAt, time.Now().UnixNano())
	<-ctx.Done()
	return nil
}
func (s *recordStartService) String() string { return s.name }

// errorService Start 立即返回错误。
type errorService struct{ name string }

func (s *errorService) Start(_ context.Context) error {
	return fmt.Errorf("%s: intentional startup error", s.name)
}
func (s *errorService) String() string { return s.name }

// wrappedDiscoverService 包装 readyService 使其同时实现 discover.Service。
type wrappedDiscoverService struct {
	*readyService
	name string
}

func (w *wrappedDiscoverService) ID() string                    { return w.name }
func (w *wrappedDiscoverService) Name() string                  { return w.name }
func (w *wrappedDiscoverService) Kind() string                  { return "test" }
func (w *wrappedDiscoverService) Addr() string                  { return "127.0.0.1:0" }
func (w *wrappedDiscoverService) Metadata() map[string]string   { return nil }

// orderingService 实现 discover.Service，并记录 Start 返回（server 停止）的时间。
type orderingService struct {
	name      string
	stoppedAt atomic.Int64
}

func (s *orderingService) Start(ctx context.Context) error {
	<-ctx.Done()
	s.stoppedAt.Store(time.Now().UnixNano())
	return nil
}
func (s *orderingService) String() string              { return s.name }
func (s *orderingService) ID() string                  { return s.name }
func (s *orderingService) Name() string                { return s.name }
func (s *orderingService) Kind() string                { return "test" }
func (s *orderingService) Addr() string                { return "127.0.0.1:0" }
func (s *orderingService) Metadata() map[string]string { return nil }

// orderingRegistry 记录注销（返回的 CancelFunc）被调用的时间。
type orderingRegistry struct {
	deregAt atomic.Int64
}

func (r *orderingRegistry) Register(_ context.Context, _ discover.Service) (context.CancelFunc, error) {
	return func() { r.deregAt.Store(time.Now().UnixNano()) }, nil
}

// 验证优雅退出顺序：先注销 → 排空(drainDelay) → 再停 server。
func TestGracefulShutdown_DeregisterBeforeStop(t *testing.T) {
	svc := &orderingService{name: "svc"}
	reg := &orderingRegistry{}
	ctx, cancel := context.WithCancel(context.Background())

	app := New(WithService(svc), WithRegistry(reg), WithShutdownDrainDelay(50*time.Millisecond))
	done := make(chan struct{})
	go func() { _ = app.Start(ctx); close(done) }()

	waitReady(t, app)
	cancel() // 触发 shutdown

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("app.Start did not return")
	}

	dereg := reg.deregAt.Load()
	stop := svc.stoppedAt.Load()
	if dereg == 0 {
		t.Fatal("deregister was never called")
	}
	if stop == 0 {
		t.Fatal("service never stopped")
	}
	if dereg >= stop {
		t.Fatalf("deregister must happen before server stop: dereg=%d stop=%d", dereg, stop)
	}
	if gap := time.Duration(stop - dereg); gap < 40*time.Millisecond {
		t.Fatalf("drain delay not honored: gap=%v (want >= ~50ms)", gap)
	}
}
