package afterwork_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/afterwork"
)

func TestDefer_RunsAfterResponse(t *testing.T) {
	var ran atomic.Int32
	reg := afterwork.New()
	ctx := afterwork.WithRegistry(context.Background(), reg)
	afterwork.Defer(ctx, func(ctx context.Context) {
		ran.Add(1)
	})
	if got := ran.Load(); got != 0 {
		t.Fatalf("task should not run synchronously, ran=%d", got)
	}
	reg.Wait()
	if got := ran.Load(); got != 1 {
		t.Fatalf("task should run after Wait, ran=%d", got)
	}
}

func TestDefer_NoRegistry_NoOp(t *testing.T) {
	// ctx 没有 Registry,Defer 不 panic、不阻塞。
	afterwork.Defer(context.Background(), func(ctx context.Context) {
		t.Fatal("should not run without registry")
	})
}

func TestWait_Idempotent(t *testing.T) {
	reg := afterwork.New()
	ctx := afterwork.WithRegistry(context.Background(), reg)
	var n atomic.Int32
	for range 5 {
		afterwork.Defer(ctx, func(context.Context) { n.Add(1) })
	}
	reg.Wait()
	reg.Wait() // 第二次立即返回
	if got := n.Load(); got != 5 {
		t.Fatalf("all 5 tasks should run, got %d", got)
	}
}

func TestWait_DrainTimeout_GivesUp(t *testing.T) {
	reg := afterwork.New(afterwork.WithDrainTimeout(50 * time.Millisecond))
	ctx := afterwork.WithRegistry(context.Background(), reg)
	started := make(chan struct{})
	afterwork.Defer(ctx, func(context.Context) {
		close(started)
		time.Sleep(500 * time.Millisecond) // 远超 drain
	})
	reg.Wait()
	select {
	case <-started:
	default:
		t.Fatal("slow task should still have started")
	}
	// Wait 已返回但任务可能仍在跑;后续 goroutine 自行结束,不阻塞测试退出。
}

func TestDefer_TaskPanic_DoesNotCrash(t *testing.T) {
	panicked := make(chan error, 1)
	reg := afterwork.New(afterwork.WithPanicHandler(func(err error) {
		panicked <- err
	}))
	ctx := afterwork.WithRegistry(context.Background(), reg)
	afterwork.Defer(ctx, func(context.Context) { panic("boom") })
	// onPanic 在 wg.Done 之后才跑(嵌套 defer 的 LIFO 顺序),
	// 所以不能靠 Wait() 同步——直接等 panic 信号。
	select {
	case err := <-panicked:
		if err == nil {
			t.Fatal("panic err should be non-nil")
		}
	case <-time.After(time.Second):
		t.Fatal("panic handler should be called")
	}
	reg.Wait()
}

func TestDefer_TaskCtxNotCancelled(t *testing.T) {
	// 即便请求 ctx 取消,延寿任务的 ctx 不应被取消(响应后仍跑完)。
	reg := afterwork.New()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = afterwork.WithRegistry(ctx, reg)
	cancel() // 请求已取消
	done := make(chan struct{})
	afterwork.Defer(ctx, func(taskCtx context.Context) {
		if taskCtx.Err() != nil {
			t.Errorf("task ctx should not be cancelled, got %v", taskCtx.Err())
		}
		close(done)
	})
	reg.Wait()
	<-done
}

func TestStop_AliasOfWait(t *testing.T) {
	reg := afterwork.New()
	ctx := afterwork.WithRegistry(context.Background(), reg)
	var n atomic.Int32
	afterwork.Defer(ctx, func(context.Context) { n.Add(1) })
	reg.Stop()
	if got := n.Load(); got != 1 {
		t.Fatalf("Stop should wait, got %d", got)
	}
}

func TestPending_TracksRunning(t *testing.T) {
	reg := afterwork.New()
	ctx := afterwork.WithRegistry(context.Background(), reg)
	block := make(chan struct{})
	afterwork.Defer(ctx, func(context.Context) { <-block })
	// 给一点时间让任务起来。
	time.Sleep(20 * time.Millisecond)
	if got := reg.Pending(); got != 1 {
		t.Fatalf("pending want 1, got %d", got)
	}
	close(block)
	reg.Wait()
	if got := reg.Pending(); got != 0 {
		t.Fatalf("pending want 0 after wait, got %d", got)
	}
}

func TestDefer_AfterStop_Dropped(t *testing.T) {
	reg := afterwork.New()
	ctx := afterwork.WithRegistry(context.Background(), reg)
	reg.Stop()
	var ran atomic.Int32
	afterwork.Defer(ctx, func(context.Context) { ran.Add(1) })
	reg.Wait()
	if got := ran.Load(); got != 0 {
		t.Fatalf("task after Stop should be dropped, got %d", got)
	}
}

func TestDefer_Concurrent(t *testing.T) {
	reg := afterwork.New()
	ctx := afterwork.WithRegistry(context.Background(), reg)
	var n atomic.Int32
	for range 50 {
		afterwork.Defer(ctx, func(context.Context) { n.Add(1) })
	}
	reg.Wait()
	if got := n.Load(); got != 50 {
		t.Fatalf("concurrent: want 50, got %d", got)
	}
}

func TestDefer_NilFn_NoOp(t *testing.T) {
	reg := afterwork.New()
	ctx := afterwork.WithRegistry(context.Background(), reg)
	afterwork.Defer(ctx, nil) // 不 panic
	(reg).Defer(ctx, nil)
	reg.Wait()
}

func TestMiddleware_WaitsForDeferred(t *testing.T) {
	var ran atomic.Int32
	h := afterwork.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		afterwork.Defer(r.Context(), func(context.Context) {
			time.Sleep(10 * time.Millisecond)
			ran.Add(1)
		})
		w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	// ServeHTTP 返回时延寿任务应已跑完(中间件已 Wait)。
	if got := ran.Load(); got != 1 {
		t.Fatalf("deferred task should complete before ServeHTTP returns, got %d", got)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body want ok, got %q", rec.Body.String())
	}
}
