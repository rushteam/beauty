package gameroom_test

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/gameroom"
)

func TestRoomLifecycle(t *testing.T) {
	var ran bool
	m := gameroom.New(gameroom.WithHooks(gameroom.Hooks{
		OnRunning: func(ctx context.Context, roomID string) error {
			ran = true
			return nil
		},
	}))
	defer m.Stop()

	h, err := m.Allocate(gameroom.Spec{ID: "r1", MaxPlayers: 2})
	if err != nil || h.Phase != gameroom.PhaseWaiting {
		t.Fatalf("allocate: err=%v phase=%s", err, h.Phase)
	}
	if err := m.Join("r1", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Join("r1", "p2"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("OnRunning not called")
	}
	got := m.Get("r1")
	if got == nil || got.Phase != gameroom.PhaseRunning {
		t.Fatalf("phase = %v", got)
	}
	if err := m.Drain("r1"); err != nil {
		t.Fatal(err)
	}
	_ = m.Leave("r1", "p1")
	_ = m.Leave("r1", "p2")
	if m.Get("r1") != nil {
		t.Fatal("expected room closed after empty")
	}
}

func TestDrainWaitingWithPlayersSkipsOnDrain(t *testing.T) {
	var drainCalled bool
	m := gameroom.New(gameroom.WithHooks(gameroom.Hooks{
		OnDrain: func(string) { drainCalled = true },
	}))
	defer m.Stop()
	_, err := m.Allocate(gameroom.Spec{ID: "r4", MaxPlayers: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Join("r4", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Drain("r4"); err != nil {
		t.Fatal(err)
	}
	if drainCalled {
		t.Fatal("OnDrain should not fire for non-running room")
	}
	if m.Get("r4") == nil {
		t.Fatal("room with players should remain")
	}
}

func TestDrainEmptyWaitingRoom(t *testing.T) {
	m := gameroom.New()
	defer m.Stop()
	_, err := m.Allocate(gameroom.Spec{ID: "r3", MaxPlayers: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Drain("r3"); err != nil {
		t.Fatal(err)
	}
	if m.Get("r3") != nil {
		t.Fatal("expected empty waiting room removed after drain")
	}
}

func TestScheduleStart(t *testing.T) {
	m := gameroom.New()
	defer m.Stop()
	_, _ = m.Allocate(gameroom.Spec{ID: "r2", MaxPlayers: 1})
	_ = m.Join("r2", "p1")
	if err := m.ScheduleStart("r2", 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	got := m.Get("r2")
	if got == nil || got.Phase != gameroom.PhaseRunning {
		t.Fatalf("phase = %v", got)
	}
}
