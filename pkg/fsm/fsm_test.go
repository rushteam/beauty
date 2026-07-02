package fsm_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/fsm"
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
)

func newMatchFSM() *fsm.FSM[matchState, matchEvent] {
	return fsm.NewBuilder[matchState, matchEvent](waiting).
		Allow(waiting, start, playing).
		Allow(playing, finish, settled).
		Allow(settled, reset, waiting).
		Build()
}

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
	// waiting 状态不能 finish
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
	// 状态不应改变
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
	// 自环转移:任意次 Fire 都合法,验证并发无 race。
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
