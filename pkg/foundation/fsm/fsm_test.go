package fsm_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/foundation/fsm"
)

// 对局状态机:等待 → 进行 → 结算。
type matchState int

const (
	waiting matchState = iota
	playing
	settled
)

type matchEvent int

const (
	start matchEvent = iota
	finish
	reset
	timeout
)

func newMatchFSM() *fsm.FSM[matchState, matchEvent] {
	return fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, playing).
		Allow(playing, finish, settled).
		Allow(settled, reset, waiting).
		Build()
}

// ===== 原有测试(向后兼容) =====

func TestFire_ValidTransitions(t *testing.T) {
	m := newMatchFSM()
	if !m.Is(waiting) {
		t.Fatal("initial should be waiting")
	}
	if s, err := m.Fire(start); err != nil || s != playing {
		t.Fatalf("start: (%v,%v)", s, err)
	}
	if s, err := m.Fire(finish); err != nil || s != settled {
		t.Fatalf("finish: (%v,%v)", s, err)
	}
	if s, err := m.Fire(reset); err != nil || s != waiting {
		t.Fatalf("reset: (%v,%v)", s, err)
	}
}

func TestFire_InvalidTransitionRejected(t *testing.T) {
	m := newMatchFSM()
	if _, err := m.Fire(finish); err == nil {
		t.Fatal("expected invalid transition error")
	}
	_, err := m.Fire(finish)
	var ite fsm.ErrInvalidTransition[matchState, matchEvent]
	if !errors.As(err, &ite) {
		t.Fatalf("want ErrInvalidTransition, got %T: %v", err, err)
	}
	if ite.From != waiting || ite.Event != finish {
		t.Fatalf("error fields: from=%v event=%v", ite.From, ite.Event)
	}
	if !m.Is(waiting) {
		t.Fatal("state should be unchanged after invalid transition")
	}
}

func TestCan_And_AvailableEvents(t *testing.T) {
	m := newMatchFSM()
	if !m.Can(start) {
		t.Fatal("waiting should allow start")
	}
	if m.Can(finish) {
		t.Fatal("waiting should not allow finish")
	}
	evs := m.AvailableEvents()
	if len(evs) != 1 || evs[0] != start {
		t.Fatalf("available events = %v, want [start]", evs)
	}
}

func TestHooks_OrderAndValues(t *testing.T) {
	var log []string
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, playing).
		OnLeave(func(from matchState, e matchEvent) error {
			log = append(log, "leave")
			return nil
		}).
		OnTransition(func(from, to matchState, e matchEvent) error {
			log = append(log, "transition")
			return nil
		}).
		OnEnter(func(to matchState, e matchEvent) error {
			log = append(log, "enter")
			return nil
		}).
		Build()

	m.Fire(start)
	want := []string{"leave", "transition", "enter"}
	if len(log) != 3 || log[0] != want[0] || log[1] != want[1] || log[2] != want[2] {
		t.Fatalf("hook order = %v, want %v", log, want)
	}
}

func TestHooks_OnLeaveAbortsTransition(t *testing.T) {
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, playing).
		OnLeave(func(from matchState, e matchEvent) error {
			return errors.New("veto")
		}).
		Build()

	s, err := m.Fire(start)
	if err == nil {
		t.Fatal("OnLeave veto should abort")
	}
	if s != waiting || !m.Is(waiting) {
		t.Fatalf("state should stay waiting, got %v", s)
	}
}

func TestReschedule_AllowOverwrites(t *testing.T) {
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, settled). // 先声明到 settled
		Allow(waiting, start, playing). // 覆盖为 playing
		Build()
	if s, _ := m.Fire(start); s != playing {
		t.Fatalf("later Allow should win, got %v", s)
	}
}

func TestFire_ConcurrentSafe(t *testing.T) {
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, waiting).
		Build()

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			for range 100 {
				m.Fire(start)
				m.Current()
				m.Can(start)
			}
		})
	}
	wg.Wait()
	if !m.Is(waiting) {
		t.Fatal("self-loop should stay in waiting")
	}
}

// ===== Guard 守卫条件 =====

func TestGuard_AllowIf_Pass(t *testing.T) {
	ready := true
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		AllowIf(waiting, start, playing, func(from matchState, e matchEvent) bool {
			return ready
		}).
		Build()
	s, err := m.Fire(start)
	if err != nil || s != playing {
		t.Fatalf("guard pass: (%v, %v)", s, err)
	}
}

func TestGuard_AllowIf_Reject(t *testing.T) {
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		AllowIf(waiting, start, playing, func(from matchState, e matchEvent) bool {
			return false // 永远不通过
		}).
		Build()
	s, err := m.Fire(start)
	var gr fsm.ErrGuardRejected[matchState, matchEvent]
	if !errors.As(err, &gr) {
		t.Fatalf("want ErrGuardRejected, got %T: %v", err, err)
	}
	if s != waiting {
		t.Fatalf("state should be unchanged, got %v", s)
	}
}

func TestGuard_MultipleGuards_FirstMatchWins(t *testing.T) {
	// 两个 guard:第一个拒绝,第二个通过(不同目标)
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		AllowIf(waiting, start, settled, func(from matchState, e matchEvent) bool {
			return false
		}).
		AllowIf(waiting, start, playing, func(from matchState, e matchEvent) bool {
			return true
		}).
		Build()
	s, err := m.Fire(start)
	if err != nil || s != playing {
		t.Fatalf("second guard should win: (%v, %v)", s, err)
	}
}

func TestGuard_MixAllowAndAllowIf(t *testing.T) {
	// AllowIf 带条件 + Allow 覆盖
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		AllowIf(waiting, start, settled, func(from matchState, e matchEvent) bool {
			return false
		}).
		Allow(waiting, start, playing). // 覆盖:清除 guard,无条件到 playing
		Build()
	s, _ := m.Fire(start)
	if s != playing {
		t.Fatalf("Allow should overwrite AllowIf, got %v", s)
	}
}

func TestGuard_DynamicCondition(t *testing.T) {
	var counter int
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		AllowIf(waiting, start, playing, func(from matchState, e matchEvent) bool {
			counter++
			return counter >= 3 // 第3次才通过
		}).
		Build()
	// 前2次被拒
	for i := 0; i < 2; i++ {
		_, err := m.Fire(start)
		if err == nil {
			t.Fatalf("fire #%d should be rejected", i+1)
		}
	}
	// 第3次通过
	s, err := m.Fire(start)
	if err != nil || s != playing {
		t.Fatalf("fire #3 should pass: (%v, %v)", s, err)
	}
}

// ===== HSM 层次状态机 =====

type doorState int

const (
	closed doorState = iota
	opened
	locked
	unlocked // locked 的子状态
)

type doorEvent int

const (
	open doorEvent = iota
	close_
	lock
	unlock
	alarm // 全局事件,所有状态响应
)

func TestHSM_EventBubblesToParent(t *testing.T) {
	// locked 是 closed 的子状态;closed 有 alarm→opened;locked 没有 alarm 转移
	// 在 locked 状态 fire alarm 应冒泡到 closed 的 alarm→opened
	m := fsm.NewBuilder[doorState, doorEvent](locked).
		Allow(closed, open, opened).
		Allow(closed, alarm, opened).
		Allow(opened, close_, closed).
		Allow(closed, lock, locked).
		Allow(locked, unlock, closed).
		Parent(locked, closed). // locked 是 closed 的子状态
		Build()

	// locked 状态 fire alarm → 冒泡到 closed → opened
	s, err := m.Fire(alarm)
	if err != nil {
		t.Fatalf("alarm should bubble to parent: %v", err)
	}
	if s != opened {
		t.Fatalf("should transition to opened, got %v", s)
	}
}

func TestHSM_DirectTransitionTakesPriority(t *testing.T) {
	// locked 有自己的 unlock 转移,不应冒泡
	m := fsm.NewBuilder[doorState, doorEvent](locked).
		Allow(closed, open, opened).
		Allow(locked, unlock, closed).
		Parent(locked, closed).
		Build()

	s, err := m.Fire(unlock)
	if err != nil || s != closed {
		t.Fatalf("direct transition should take priority: (%v, %v)", s, err)
	}
}

func TestHSM_IsIn(t *testing.T) {
	m := fsm.NewBuilder[doorState, doorEvent](locked).
		Allow(locked, unlock, closed).
		Parent(locked, closed).
		Build()

	// locked 是 closed 的子状态
	if !m.IsIn(closed) {
		t.Fatal("locked IsIn(closed) should be true")
	}
	if !m.IsIn(locked) {
		t.Fatal("locked IsIn(locked) should be true")
	}
	if m.IsIn(opened) {
		t.Fatal("locked IsIn(opened) should be false")
	}
}

func TestHSM_AvailableEvents_IncludesParent(t *testing.T) {
	m := fsm.NewBuilder[doorState, doorEvent](locked).
		Allow(closed, alarm, opened).  // 父状态的事件
		Allow(locked, unlock, closed). // 子状态自己的事件
		Parent(locked, closed).
		Build()

	evs := m.AvailableEvents()
	if len(evs) != 2 {
		t.Fatalf("available events = %v, want 2 events", evs)
	}
}

// ===== 状态超时 =====

func TestTimeout_AutoFire(t *testing.T) {
	var fired atomic.Bool
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, playing).
		Allow(playing, timeout, settled).
		Timeout(playing, 50*time.Millisecond, timeout).
		OnEnter(func(to matchState, e matchEvent) error {
			if to == settled {
				fired.Store(true)
			}
			return nil
		}).
		Build()
	defer m.Close()

	m.Fire(start) // → playing,启动 50ms 超时
	time.Sleep(150 * time.Millisecond)

	if !fired.Load() {
		t.Fatal("timeout should have auto-fired")
	}
	if !m.Is(settled) {
		t.Fatalf("should be settled, got %v", m.Current())
	}
}

func TestTimeout_ResetOnReentry(t *testing.T) {
	var count atomic.Int32
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, playing).
		Allow(playing, reset, waiting).
		Allow(playing, timeout, settled).
		Timeout(playing, 80*time.Millisecond, timeout).
		OnEnter(func(to matchState, e matchEvent) error {
			if to == settled {
				count.Add(1)
			}
			return nil
		}).
		Build()
	defer m.Close()

	// 进入 playing,50ms 后重置回 waiting 再进 playing
	m.Fire(start) // → playing
	time.Sleep(50 * time.Millisecond)
	m.Fire(reset) // → waiting(取消旧超时)
	m.Fire(start) // → playing(启动新 80ms 超时)
	time.Sleep(50 * time.Millisecond)
	// 此时距第二次进入 playing 才 50ms,不应超时
	if m.Is(settled) {
		t.Fatal("timeout should have been reset")
	}
	time.Sleep(50 * time.Millisecond)
	// 现在应该超时了
	if !m.Is(settled) {
		t.Fatalf("should have timed out, got %v", m.Current())
	}
}

func TestTimeout_CloseStopsTimer(t *testing.T) {
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, playing).
		Allow(playing, timeout, settled).
		Timeout(playing, 50*time.Millisecond, timeout).
		Build()

	m.Fire(start) // → playing
	m.Close()     // 停止超时
	time.Sleep(100 * time.Millisecond)

	if m.Is(settled) {
		t.Fatal("Close should prevent timeout fire")
	}
}

// ===== 可视化导出 =====

func TestExportDOT(t *testing.T) {
	m := newMatchFSM()
	dot := m.ExportDOT()
	if !strings.Contains(dot, "digraph FSM") {
		t.Fatal("should contain digraph header")
	}
	if !strings.Contains(dot, "->") {
		t.Fatal("should contain transitions")
	}
	// 当前状态高亮
	if !strings.Contains(dot, "fillcolor=lightblue") {
		t.Fatal("should highlight current state")
	}
}

func TestExportDOT_WithGuard(t *testing.T) {
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		AllowIf(waiting, start, playing, func(from matchState, e matchEvent) bool { return true }).
		Build()
	dot := m.ExportDOT()
	if !strings.Contains(dot, "guard") {
		t.Fatal("guard should be labeled in DOT")
	}
}

func TestExportDOT_WithHSM(t *testing.T) {
	m := fsm.NewBuilder[doorState, doorEvent](locked).
		Allow(locked, unlock, closed).
		Parent(locked, closed).
		Build()
	dot := m.ExportDOT()
	if !strings.Contains(dot, "parent") {
		t.Fatal("HSM parent edge should be in DOT")
	}
}

func TestExportMermaid(t *testing.T) {
	m := newMatchFSM()
	md := m.ExportMermaid()
	if !strings.Contains(md, "stateDiagram-v2") {
		t.Fatal("should contain mermaid header")
	}
	if !strings.Contains(md, "-->") {
		t.Fatal("should contain transitions")
	}
}

func TestExportMermaid_WithHSM(t *testing.T) {
	m := fsm.NewBuilder[doorState, doorEvent](locked).
		Allow(locked, unlock, closed).
		Parent(locked, closed).
		Build()
	md := m.ExportMermaid()
	if !strings.Contains(md, "state") {
		t.Fatal("HSM should produce state block in Mermaid")
	}
}

func TestExportMermaid_WithTimeout(t *testing.T) {
	m := fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, playing).
		Timeout(playing, 30*time.Second, timeout).
		Build()
	defer m.Close()
	md := m.ExportMermaid()
	if !strings.Contains(md, "⏱") {
		t.Fatal("timeout should produce note in Mermaid")
	}
}
