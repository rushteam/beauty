package hedge_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/hedge"
)

// primary 卡住 → 到 delay 后补发的副本先成功返回。
func TestDo_HedgeWinsWhenPrimarySlow(t *testing.T) {
	var launches int32
	v, err := hedge.Do(context.Background(), 20*time.Millisecond, 2,
		func(ctx context.Context, attempt int) (string, error) {
			atomic.AddInt32(&launches, 1)
			if attempt == 0 {
				<-ctx.Done() // primary 永不成功
				return "", ctx.Err()
			}
			return fmt.Sprintf("attempt-%d", attempt), nil
		})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if v != "attempt-1" {
		t.Fatalf("应由对冲副本胜出, got %q", v)
	}
	if atomic.LoadInt32(&launches) < 2 {
		t.Fatalf("应至少补发过 1 个副本, launches=%d", launches)
	}
}

// primary 在 delay 前返回 → 不触发对冲(只发一次)。
func TestDo_NoHedgeWhenPrimaryFast(t *testing.T) {
	var launches int32
	v, err := hedge.Do(context.Background(), 100*time.Millisecond, 3,
		func(_ context.Context, attempt int) (string, error) {
			atomic.AddInt32(&launches, 1)
			return "ok", nil
		})
	if err != nil || v != "ok" {
		t.Fatalf("v=%q err=%v", v, err)
	}
	if n := atomic.LoadInt32(&launches); n != 1 {
		t.Fatalf("primary 快速返回不应对冲, launches=%d", n)
	}
}

// 全部失败 → 返回错误。
func TestDo_AllFail(t *testing.T) {
	wantErr := errors.New("boom")
	var launches int32
	_, err := hedge.Do(context.Background(), 5*time.Millisecond, 2,
		func(_ context.Context, _ int) (int, error) {
			atomic.AddInt32(&launches, 1)
			return 0, wantErr
		})
	if !errors.Is(err, wantErr) {
		t.Fatalf("应返回底层错误, got %v", err)
	}
	if n := atomic.LoadInt32(&launches); n != 3 {
		t.Fatalf("应发满全部副本, launches=%d", n)
	}
}

// delay<=0 → 一次性并发发满(请求竞速)。
func TestDo_RaceAllImmediately(t *testing.T) {
	var launches int32
	v, err := hedge.Do(context.Background(), 0, 2,
		func(ctx context.Context, attempt int) (int, error) {
			atomic.AddInt32(&launches, 1)
			if attempt == 2 {
				return 42, nil // 最后一个立刻成功
			}
			<-ctx.Done()
			return 0, ctx.Err()
		})
	if err != nil || v != 42 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	// Do 在 attempt 2 成功即返回,阻塞的 0/1 副本此刻可能尚未被调度到入口递增计数;
	// 返回时 ctx 已取消会唤醒它们,计数最终收敛到 3。轮询等待收敛,避免读取时机竞争。
	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&launches) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := atomic.LoadInt32(&launches); n != 3 {
		t.Fatalf("delay<=0 应立即发满全部副本, launches=%d", n)
	}
}

// 父 ctx 取消 → 返回 ctx 错误。
func TestDo_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := hedge.Do(ctx, 10*time.Millisecond, 2,
		func(ctx context.Context, _ int) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		})
	if err == nil {
		t.Fatal("父 ctx 取消应返回错误")
	}
}

// maxHedge=0 → 退化为只执行一次。
func TestDo_NoHedgeConfigured(t *testing.T) {
	var launches int32
	_, _ = hedge.Do(context.Background(), time.Millisecond, 0,
		func(_ context.Context, _ int) (int, error) {
			atomic.AddInt32(&launches, 1)
			return 1, nil
		})
	if n := atomic.LoadInt32(&launches); n != 1 {
		t.Fatalf("maxHedge=0 应只执行一次, launches=%d", n)
	}
}
