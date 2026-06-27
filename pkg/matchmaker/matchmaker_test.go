package matchmaker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMatchmaker_BasicMatch(t *testing.T) {
	var matches atomic.Int32
	var mu sync.Mutex
	var matched [][]string
	m := New(func(ctx context.Context, mm Match) error {
		mu.Lock()
		ids := make([]string, len(mm.Tickets))
		for i, tk := range mm.Tickets {
			ids[i] = tk.Presence.UserID
		}
		matched = append(matched, ids)
		mu.Unlock()
		matches.Add(1)
		return nil
	}, WithTickInterval(50*time.Millisecond))
	m.Start(context.Background())
	defer func() { m.Stop(); m.Wait() }()

	// 3 个玩家,都要 2-3 人队,同桶。
	props := Properties{
		String:  map[string]string{"region": "eu"},
		Numeric: map[string]float64{"skill": 1000},
	}
	for _, uid := range []string{"u1", "u2", "u3"} {
		m.Add(Ticket{
			Presence:   Presence{UserID: uid, SessionID: uid},
			Properties: props,
			MinCount:   2, MaxCount: 3,
		}, "5v5", "eu|ranked")
	}

	deadline := time.After(2 * time.Second)
	for {
		if matches.Load() >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("no match, count=%d", m.Count())
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(matched) != 1 || len(matched[0]) != 3 {
		t.Fatalf("matched=%+v want one team of 3", matched)
	}
	if m.Count() != 0 {
		t.Fatalf("count after match=%d want 0", m.Count())
	}
}

func TestMatchmaker_Remove(t *testing.T) {
	m := New(func(ctx context.Context, mm Match) error { return nil }, WithTickInterval(50*time.Millisecond))
	m.Start(context.Background())
	defer func() { m.Stop(); m.Wait() }()

	id, _ := m.Add(Ticket{
		Presence:   Presence{UserID: "u1"},
		Properties: Properties{String: map[string]string{"r": "a"}},
		MinCount:   5, MaxCount: 5, // 凑不齐,不会匹配
	}, "p", "a|b")
	if m.Count() != 1 {
		t.Fatalf("count=%d want 1", m.Count())
	}
	if !m.Remove(id) {
		t.Fatal("Remove returned false")
	}
	if m.Count() != 0 {
		t.Fatalf("count after remove=%d want 0", m.Count())
	}
}

func TestMatchmaker_InvalidRange(t *testing.T) {
	m := New(func(ctx context.Context, mm Match) error { return nil })
	_, err := m.Add(Ticket{MinCount: 5, MaxCount: 3}, "p", "b")
	if err == nil {
		t.Fatal("expected error for invalid range")
	}
}

func TestMatchmaker_QueryNearBySkill(t *testing.T) {
	m := New(func(ctx context.Context, mm Match) error { return nil }, WithTickInterval(time.Hour))
	m.Start(context.Background())
	defer func() { m.Stop(); m.Wait() }()

	for i, skill := range []float64{100, 200, 300, 400, 500} {
		m.Add(Ticket{
			Presence:   Presence{UserID: "u" + string(rune('1'+i))},
			Properties: Properties{Numeric: map[string]float64{"skill": skill}},
			MinCount:   10, MaxCount: 10, // 不匹配
		}, "pool", "b")
	}
	near := m.QueryNearBySkill("pool", 310, 3)
	if len(near) != 3 {
		t.Fatalf("near len=%d want 3", len(near))
	}
	// 最接近 310 的应是 300, 400, 200。
	if near[0].Properties.Numeric["skill"] != 300 {
		t.Fatalf("nearest=%v want 300", near[0].Properties.Numeric["skill"])
	}
}
