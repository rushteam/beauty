package xgo

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBasicGo(t *testing.T) {
	p := New()
	defer p.Close()

	var counter int32
	var wg sync.WaitGroup

	// 提交多个任务
	for i := 0; i < 10; i++ {
		wg.Add(1)
		p.Go(func() {
			defer wg.Done()
			atomic.AddInt32(&counter, 1)
		})
	}

	wg.Wait()

	if counter != 10 {
		t.Errorf("expected counter to be 10, got %d", counter)
	}
}

func TestNewWaitGroup(t *testing.T) {
	p := New()
	defer p.Close()

	var counter int32

	// 使用新的 WaitGroup API
	wg := p.NewWaitGroup()

	// 提交多个任务到同一个 WaitGroup
	for i := 0; i < 5; i++ {
		wg.Go(func() {
			time.Sleep(time.Millisecond * 10)
			atomic.AddInt32(&counter, 1)
		})
	}

	// 等待所有任务完成
	wg.Wait()

	if counter != 5 {
		t.Errorf("expected counter to be 5, got %d", counter)
	}
}

func TestWaitGroupWithContext(t *testing.T) {
	p := New()
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*50)
	defer cancel()

	var executed int32
	wg := p.NewWaitGroup()

	// 提交一个长时间运行的任务
	wg.GoWithContext(ctx, func(ctx context.Context) {
		select {
		case <-ctx.Done():
			// 任务被取消
			return
		case <-time.After(time.Millisecond * 100):
			// 任务完成（不应该到达这里）
			atomic.StoreInt32(&executed, 1)
		}
	})

	wg.Wait()

	if executed == 1 {
		t.Error("task should have been cancelled by context")
	}
}

func TestPanicHandling(t *testing.T) {
	var panicCaught bool
	var panicValue any

	p := New(WithPanicHandler(func(taskName string, pv any, stack []byte) {
		panicCaught = true
		panicValue = pv
		// taskName 在这个测试中为空，因为我们没有设置任务名称
	}))
	defer p.Close()

	wg := p.NewWaitGroup()
	wg.Go(func() {
		panic("test panic")
	})

	wg.Wait()
	time.Sleep(time.Millisecond * 10) // 给 panic handler 一点时间

	if !panicCaught {
		t.Error("panic should have been caught")
	}

	if panicValue != "test panic" {
		t.Errorf("expected panic value 'test panic', got %v", panicValue)
	}
}

func TestWorkerScaling(t *testing.T) {
	p := New(WithSetCap(5), WithScaleThreshold(2))
	defer p.Close()

	// 提交大量任务
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		p.Go(func() {
			defer wg.Done()
			time.Sleep(time.Millisecond * 10)
		})
	}

	// 检查 worker 数量是否在合理范围内
	time.Sleep(time.Millisecond * 5)
	workers := p.Workers()
	if workers <= 0 || workers > 5 {
		t.Errorf("expected workers to be between 1 and 5, got %d", workers)
	}

	wg.Wait()
}

func TestPoolClose(t *testing.T) {
	p := New()

	var counter int32
	wg := p.NewWaitGroup()

	// 提交一些任务
	for i := 0; i < 5; i++ {
		wg.Go(func() {
			time.Sleep(time.Millisecond * 20)
			atomic.AddInt32(&counter, 1)
		})
	}

	// 关闭协程池
	go func() {
		time.Sleep(time.Millisecond * 10)
		p.Close()
	}()

	wg.Wait()

	if counter != 5 {
		t.Errorf("expected all tasks to complete, got %d/5", counter)
	}

	// 尝试提交新任务应该失败（不会执行）
	newWg := p.NewWaitGroup()
	newWg.Go(func() {
		atomic.AddInt32(&counter, 1)
	})

	// 等待一小段时间，确保任务不会被执行
	time.Sleep(time.Millisecond * 10)

	if counter != 5 {
		t.Error("task should not be executed on closed pool")
	}
}

func TestPoolCloseWithTimeout(t *testing.T) {
	p := New()

	// 提交一个长时间运行的任务
	wg := p.NewWaitGroup()
	wg.Go(func() {
		time.Sleep(time.Second) // 很长的任务
	})

	// 等待任务开始执行
	time.Sleep(time.Millisecond * 10)

	// 尝试带超时关闭
	start := time.Now()
	err := p.CloseWithTimeout(time.Millisecond * 100)
	duration := time.Since(start)

	if err == nil {
		t.Error("expected timeout error")
	}

	if duration < time.Millisecond*90 || duration > time.Millisecond*150 {
		t.Errorf("expected timeout around 100ms, got %v", duration)
	}
}

func TestGlobalWaitGroup(t *testing.T) {
	var counter int32

	// 测试全局 WaitGroup
	wg := NewWaitGroup()

	// 提交多个任务
	for i := 0; i < 3; i++ {
		wg.Go(func() {
			atomic.AddInt32(&counter, 1)
		})
	}

	wg.Wait()

	if counter != 3 {
		t.Errorf("expected counter to be 3, got %d", counter)
	}

	// 测试 SafeGoWithContext
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel()

	wg2 := NewWaitGroup()
	wg2.GoWithContext(ctx, func(ctx context.Context) {
		select {
		case <-ctx.Done():
			// 上下文被取消，不增加计数器
			return
		default:
			atomic.AddInt32(&counter, 1)
		}
	})

	wg2.Wait()

	// counter 应该是 3 或 4，取决于第二个任务是否在超时前完成
	if counter < 3 {
		t.Errorf("expected counter to be at least 3, got %d", counter)
	}
}

func TestErrorGroup(t *testing.T) {
	pool := New()
	defer pool.Close()

	t.Run("all success", func(t *testing.T) {
		eg := pool.NewErrorGroup()
		var counter int32

		// 提交多个成功的任务
		for i := 0; i < 5; i++ {
			eg.Go(func() error {
				atomic.AddInt32(&counter, 1)
				return nil
			})
		}

		err := eg.Wait()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if counter != 5 {
			t.Errorf("expected counter to be 5, got %d", counter)
		}
	})

	t.Run("first error", func(t *testing.T) {
		eg := pool.NewErrorGroup()
		var counter int32

		// 提交一些任务，其中一个会失败
		for i := 0; i < 5; i++ {
			taskID := i
			eg.Go(func() error {
				atomic.AddInt32(&counter, 1)
				if taskID == 2 {
					return fmt.Errorf("task %d failed", taskID)
				}
				return nil
			})
		}

		err := eg.Wait()
		if err == nil {
			t.Error("expected an error, got nil")
		}

		if err.Error() != "task 2 failed" {
			t.Errorf("expected 'task 2 failed', got %v", err)
		}

		// 所有任务都应该执行（因为错误检查在任务完成后）
		if counter != 5 {
			t.Errorf("expected counter to be 5, got %d", counter)
		}
	})
}

func TestErrorGroupWithContext(t *testing.T) {
	pool := New()
	defer pool.Close()

	t.Run("context cancellation", func(t *testing.T) {
		eg, ctx := pool.NewErrorGroupWithContext(context.Background())
		var executed int32

		// 提交一个会失败的任务，应该取消其他任务
		eg.Go(func() error {
			time.Sleep(time.Millisecond * 10)
			return fmt.Errorf("first task failed")
		})

		// 提交其他任务，应该被取消
		for i := 0; i < 3; i++ {
			eg.GoWithContext(ctx, func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Millisecond * 100):
					atomic.AddInt32(&executed, 1)
					return nil
				}
			})
		}

		err := eg.Wait()
		if err == nil {
			t.Error("expected an error, got nil")
		}

		// 由于快速失败，其他任务应该被取消
		if executed > 0 {
			t.Errorf("expected no tasks to complete due to cancellation, got %d", executed)
		}
	})
}

func TestGlobalErrorGroup(t *testing.T) {
	t.Run("global error group", func(t *testing.T) {
		eg := NewErrorGroup()
		var counter int32

		// 提交任务
		for i := 0; i < 3; i++ {
			taskID := i
			eg.Go(func() error {
				atomic.AddInt32(&counter, 1)
				if taskID == 1 {
					return fmt.Errorf("task %d failed", taskID)
				}
				return nil
			})
		}

		err := eg.Wait()
		if err == nil {
			t.Error("expected an error, got nil")
		}

		if counter != 3 {
			t.Errorf("expected counter to be 3, got %d", counter)
		}
	})

	t.Run("global error group with context", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*50)
		defer cancel()

		eg, egCtx := NewErrorGroupWithContext(ctx)
		var completed int32

		// 提交任务
		for i := 0; i < 3; i++ {
			eg.GoWithContext(egCtx, func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Millisecond * 100):
					atomic.AddInt32(&completed, 1)
					return nil
				}
			})
		}

		err := eg.Wait()
		if err == nil {
			t.Error("expected timeout error, got nil")
		}

		// 由于超时，任务应该被取消
		if completed > 0 {
			t.Errorf("expected no tasks to complete due to timeout, got %d", completed)
		}
	})
}

func TestPendingTasks(t *testing.T) {
	p := New(WithSetCap(1)) // 限制只有1个worker
	defer p.Close()

	// 提交多个任务
	for i := 0; i < 5; i++ {
		p.Go(func() {
			time.Sleep(time.Millisecond * 50)
		})
	}

	time.Sleep(time.Millisecond * 10) // 让第一个任务开始执行

	pending := p.PendingTasks()
	if pending <= 0 {
		t.Errorf("expected pending tasks > 0, got %d", pending)
	}
}

func BenchmarkPoolWaitGroup(b *testing.B) {
	p := New()
	defer p.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			wg := p.NewWaitGroup()
			wg.Go(func() {
				// 模拟一些工作
				time.Sleep(time.Microsecond)
			})
			wg.Wait()
		}
	})
}

func BenchmarkGlobalWaitGroup(b *testing.B) {
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			wg := NewWaitGroup()
			wg.Go(func() {
				// 模拟一些工作
				time.Sleep(time.Microsecond)
			})
			wg.Wait()
		}
	})
}
