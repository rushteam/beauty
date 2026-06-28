package txn_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/txn"
)

// fakeParticipant 一个可观测的假 Participant:记录各阶段被调用。
type fakeParticipant struct {
	name      string
	prepareOK bool
	commitOK  bool
	prepare   atomic.Int32
	commit    atomic.Int32
	rollback  atomic.Int32
}

func (f *fakeParticipant) Prepare(ctx context.Context) error {
	f.prepare.Add(1)
	if !f.prepareOK {
		return errors.New(f.name + " prepare failed")
	}
	return nil
}
func (f *fakeParticipant) Commit(ctx context.Context) error {
	f.commit.Add(1)
	if !f.commitOK {
		return errors.New(f.name + " commit failed")
	}
	return nil
}
func (f *fakeParticipant) Rollback(ctx context.Context) error {
	f.rollback.Add(1)
	return nil
}

func TestRun_AllPrepared_AllCommitted(t *testing.T) {
	a := &fakeParticipant{name: "a", prepareOK: true, commitOK: true}
	b := &fakeParticipant{name: "b", prepareOK: true, commitOK: true}
	c := txn.New()
	c.Enlist("a", a)
	c.Enlist("b", b)
	bodyRan := false
	err := c.Run(context.Background(), func() error { bodyRan = true; return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bodyRan {
		t.Fatal("body should run")
	}
	if a.commit.Load() != 1 || b.commit.Load() != 1 {
		t.Fatal("both should commit")
	}
	if a.rollback.Load() != 0 || b.rollback.Load() != 0 {
		t.Fatal("no rollback on success")
	}
}

func TestRun_PrepareFails_RollbackPrepared(t *testing.T) {
	a := &fakeParticipant{name: "a", prepareOK: true, commitOK: true}
	b := &fakeParticipant{name: "b", prepareOK: false, commitOK: true} // b Prepare 失败
	c := txn.New()
	c.Enlist("a", a)
	c.Enlist("b", b)
	bodyRan := false
	err := c.Run(context.Background(), func() error { bodyRan = true; return nil })
	if err == nil {
		t.Fatal("want error from prepare failure")
	}
	if bodyRan {
		t.Fatal("body should not run when prepare fails")
	}
	// a 已 Prepare,应被回滚;b 没 Prepare 成功,不回滚(但 Rollback 被调用与否取决于实现,
	// 这里 b 的 Prepare 失败不在 prepared 列表)。
	if a.rollback.Load() != 1 {
		t.Fatal("a should be rolled back")
	}
	if a.commit.Load() != 0 || b.commit.Load() != 0 {
		t.Fatal("no commit on prepare failure")
	}
}

func TestRun_BodyFails_RollbackAll(t *testing.T) {
	a := &fakeParticipant{name: "a", prepareOK: true, commitOK: true}
	b := &fakeParticipant{name: "b", prepareOK: true, commitOK: true}
	c := txn.New()
	c.Enlist("a", a)
	c.Enlist("b", b)
	err := c.Run(context.Background(), func() error {
		return errors.New("body boom")
	})
	if err == nil {
		t.Fatal("want body error")
	}
	if a.rollback.Load() != 1 || b.rollback.Load() != 1 {
		t.Fatal("all prepared should rollback on body failure")
	}
	if a.commit.Load() != 0 || b.commit.Load() != 0 {
		t.Fatal("no commit on body failure")
	}
}

func TestRun_CommitFails_BestEffort(t *testing.T) {
	a := &fakeParticipant{name: "a", prepareOK: true, commitOK: false} // a Commit 失败
	b := &fakeParticipant{name: "b", prepareOK: true, commitOK: true}
	c := txn.New()
	c.Enlist("a", a)
	c.Enlist("b", b)
	err := c.Run(context.Background(), func() error { return nil })
	if err == nil {
		t.Fatal("want commit error")
	}
	// best-effort:a Commit 失败,但 b 仍被 Commit。
	if b.commit.Load() != 1 {
		t.Fatal("b should still commit (best-effort)")
	}
}

func TestRun_RollbackOrder_IsReverse(t *testing.T) {
	var order []string
	mk := func(name string, prepareOK bool) *trackingP {
		return &trackingP{name: name, prepareOK: prepareOK, order: &order}
	}
	a := mk("a", true)
	b := mk("b", true)
	c := txn.New()
	c.Enlist("a", a)
	c.Enlist("b", b)
	// body 失败 → 回滚。期望回滚顺序 b,a(逆序)。
	_ = c.Run(context.Background(), func() error { return errors.New("x") })
	if len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Fatalf("rollback order want [b a], got %v", order)
	}
}

type trackingP struct {
	name      string
	prepareOK bool
	order     *[]string
}

func (p *trackingP) Prepare(context.Context) error { return nil }
func (p *trackingP) Commit(context.Context) error  { return nil }
func (p *trackingP) Rollback(context.Context) error {
	*p.order = append(*p.order, p.name)
	return nil
}

func TestEnlistFunc_FunctionForm(t *testing.T) {
	var preped, committed, rolled atomic.Bool
	c := txn.New()
	c.EnlistFunc("x",
		func(context.Context) error { preped.Store(true); return nil },
		func(context.Context) error { committed.Store(true); return nil },
		func(context.Context) error { rolled.Store(true); return nil },
	)
	if err := c.Run(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !preped.Load() || !committed.Load() || rolled.Load() {
		t.Fatal("prepare+commit should run, rollback should not")
	}
}

func TestRun_PrepareOrder_IsEnlistOrder(t *testing.T) {
	var order []string
	p1 := &prepareTracker{name: "first", order: &order}
	p2 := &prepareTracker{name: "second", order: &order}
	c := txn.New()
	c.Enlist("first", p1)
	c.Enlist("second", p2)
	_ = c.Run(context.Background(), func() error { return nil })
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("prepare order want [first second], got %v", order)
	}
}

type prepareTracker struct {
	name  string
	order *[]string
}

func (p *prepareTracker) Prepare(context.Context) error {
	*p.order = append(*p.order, p.name)
	return nil
}
func (p *prepareTracker) Commit(context.Context) error  { return nil }
func (p *prepareTracker) Rollback(context.Context) error { return nil }

func TestRun_Empty_NoOp(t *testing.T) {
	c := txn.New()
	bodyRan := false
	err := c.Run(context.Background(), func() error { bodyRan = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !bodyRan {
		t.Fatal("body should run even with no participants")
	}
}

func TestRun_RunsSerially(t *testing.T) {
	// 一次只 Run 一个事务:并发 Run 应串行。
	c := txn.New()
	var inflight, maxInflight atomic.Int32
	c.EnlistFunc("p",
		func(context.Context) error { return nil },
		func(context.Context) error {
			cur := inflight.Add(1)
			for {
				old := maxInflight.Load()
				if cur <= old || maxInflight.CompareAndSwap(old, cur) {
					break
				}
			}
			inflight.Add(-1)
			return nil
		},
		nil,
	)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_ = c.Run(context.Background(), func() error { return nil })
		})
	}
	wg.Wait()
	if maxInflight.Load() > 1 {
		t.Fatalf("Run should be serial, max inflight=%d", maxInflight.Load())
	}
}
