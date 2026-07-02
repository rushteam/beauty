package saga_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/saga"
)

func noop(context.Context) error { return nil }

func TestExecute_AllSucceed(t *testing.T) {
	var log []string
	s := saga.New("ok").
		Step("a", func(context.Context) error { log = append(log, "a"); return nil }, nil).
		Step("b", func(context.Context) error { log = append(log, "b"); return nil }, nil)

	res := s.Execute(context.Background())
	if res.Status != saga.StatusCommitted || res.Failed() {
		t.Fatalf("status=%v failed=%v", res.Status, res.Failed())
	}
	if len(log) != 2 || log[0] != "a" || log[1] != "b" {
		t.Fatalf("action order = %v", log)
	}
	if len(res.Steps) != 2 || res.Steps[0].ActionErr != nil {
		t.Fatalf("steps: %+v", res.Steps)
	}
}

func TestExecute_MidFailureCompensatesInReverse(t *testing.T) {
	var comp []string
	s := saga.New("purchase").
		Step("deduct-coin", noop, func(context.Context) error { comp = append(comp, "refund-coin"); return nil }).
		Step("reserve-item", noop, func(context.Context) error { comp = append(comp, "release-item"); return nil }).
		Step("grant-card",
			func(context.Context) error { return errors.New("grant service down") },
			func(context.Context) error { comp = append(comp, "revoke-card"); return nil })

	res := s.Execute(context.Background())
	if res.Status != saga.StatusCompensated {
		t.Fatalf("status = %v, want compensated", res.Status)
	}
	if res.FailedStep != "grant-card" {
		t.Fatalf("failed step = %q", res.FailedStep)
	}
	if res.Err == nil {
		t.Fatal("Err should carry original failure")
	}
	// 只补偿已成功的前两步,逆序:release-item 先于 refund-coin。
	if len(comp) != 2 || comp[0] != "release-item" || comp[1] != "refund-coin" {
		t.Fatalf("compensation order = %v, want [release-item refund-coin]", comp)
	}
	// 失败步骤(grant-card)自身不补偿。
	for _, sr := range res.Steps {
		if sr.Name == "grant-card" && sr.Compensated {
			t.Fatal("failed step should not be compensated")
		}
	}
}

func TestExecute_NilCompensateSkipped(t *testing.T) {
	var comp []string
	s := saga.New("s").
		Step("readonly", noop, nil). // 无补偿
		Step("write", noop, func(context.Context) error { comp = append(comp, "undo-write"); return nil }).
		Step("fail", func(context.Context) error { return errors.New("boom") }, nil)

	res := s.Execute(context.Background())
	if res.Status != saga.StatusCompensated {
		t.Fatalf("status = %v", res.Status)
	}
	// 只有 write 有补偿;readonly 跳过。
	if len(comp) != 1 || comp[0] != "undo-write" {
		t.Fatalf("comp = %v", comp)
	}
}

func TestExecute_CompensationFailure(t *testing.T) {
	s := saga.New("s").
		Step("a", noop, func(context.Context) error { return errors.New("refund failed") }).
		Step("b", func(context.Context) error { return errors.New("b failed") }, nil)

	res := s.Execute(context.Background())
	if res.Status != saga.StatusCompensationFailed {
		t.Fatalf("status = %v, want compensation_failed", res.Status)
	}
	// step a 补偿失败应记录在明细里。
	var found bool
	for _, sr := range res.Steps {
		if sr.Name == "a" {
			found = true
			if !sr.Compensated || sr.CompensateErr == nil {
				t.Fatalf("step a: compensated=%v err=%v", sr.Compensated, sr.CompensateErr)
			}
		}
	}
	if !found {
		t.Fatal("step a not in results")
	}
}

func TestExecute_CompensationRetrySucceeds(t *testing.T) {
	var attempts int
	s := saga.New("s", saga.WithCompensationRetry(3, time.Millisecond)).
		Step("a", noop, func(context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("transient")
			}
			return nil // 第 3 次成功
		}).
		Step("b", func(context.Context) error { return errors.New("boom") }, nil)

	res := s.Execute(context.Background())
	if res.Status != saga.StatusCompensated {
		t.Fatalf("status = %v, want compensated (retry should recover)", res.Status)
	}
	if attempts != 3 {
		t.Fatalf("compensate attempts = %d, want 3", attempts)
	}
	for _, sr := range res.Steps {
		if sr.Name == "a" && sr.CompensateTry != 3 {
			t.Fatalf("CompensateTry = %d, want 3", sr.CompensateTry)
		}
	}
}

func TestExecute_PanicInActionTriggersCompensation(t *testing.T) {
	var compensated bool
	s := saga.New("s").
		Step("a", noop, func(context.Context) error { compensated = true; return nil }).
		Step("b", func(context.Context) error { panic("kaboom") }, nil)

	res := s.Execute(context.Background())
	if res.Status != saga.StatusCompensated {
		t.Fatalf("panic should become failure + compensate, status=%v", res.Status)
	}
	if !compensated {
		t.Fatal("step a should have been compensated after panic in b")
	}
	if res.Err == nil {
		t.Fatal("panic should surface in Err")
	}
}

func TestExecute_ContextCancelledBeforeStep(t *testing.T) {
	var aRan, comp bool
	ctx, cancel := context.WithCancel(context.Background())
	s := saga.New("s").
		Step("a", func(context.Context) error {
			aRan = true
			cancel() // 执行完 a 就取消,b 开始前应被拦下
			return nil
		}, func(context.Context) error { comp = true; return nil }).
		Step("b", func(context.Context) error { t.Error("b should not run"); return nil }, nil)

	res := s.Execute(ctx)
	if !aRan {
		t.Fatal("a should have run")
	}
	if res.Status != saga.StatusCompensated {
		t.Fatalf("status = %v, want compensated", res.Status)
	}
	if !comp {
		t.Fatal("a should be compensated after ctx cancel")
	}
}

func TestExecute_CompensationRunsDespiteCancelledCtx(t *testing.T) {
	// 补偿用 WithoutCancel:即使原 ctx 已取消,补偿仍须执行。
	ctx, cancel := context.WithCancel(context.Background())
	var compCtxErr error
	var wg sync.WaitGroup
	wg.Add(1)
	s := saga.New("s").
		Step("a", noop, func(cctx context.Context) error {
			compCtxErr = cctx.Err() // 补偿收到的 ctx 不应是 cancelled
			wg.Done()
			return nil
		}).
		Step("b", func(context.Context) error { cancel(); return errors.New("boom") }, nil)

	res := s.Execute(ctx)
	wg.Wait()
	if compCtxErr != nil {
		t.Fatalf("compensation ctx should not be cancelled, got %v", compCtxErr)
	}
	if res.Status != saga.StatusCompensated {
		t.Fatalf("status = %v", res.Status)
	}
}
