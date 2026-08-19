package matchmaker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/game/rating/glicko2"
)

func TestApplyResult_UpdatesStore(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Set("a", 1000)
	_ = store.Set("b", 1000)
	rater := func(participants []string, result map[string]float64) map[string]float64 {
		out := make(map[string]float64, len(participants))
		for _, p := range participants {
			out[p] = 1000 + 100*result[p]
		}
		return out
	}
	err := ApplyResult(store, rater, []string{"a", "b"}, map[string]float64{"a": 1, "b": 0})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.Get("a")
	b, _ := store.Get("b")
	if a != 1100 || b != 1000 {
		t.Fatalf("a=%v b=%v", a, b)
	}
}

func TestApplyResult_Glicko2Loop(t *testing.T) {
	store := NewMemoryStore()
	calc := glicko2.New()
	ratings := map[string]glicko2.Rating{
		"a": glicko2.Default(),
		"b": glicko2.Default(),
	}
	rater := func(participants []string, result map[string]float64) map[string]float64 {
		out := make(map[string]float64, len(participants))
		next := make(map[string]glicko2.Rating, len(participants))
		for _, p := range participants {
			var outcomes []glicko2.Outcome
			for _, o := range participants {
				if o == p {
					continue
				}
				outcomes = append(outcomes, glicko2.Outcome{Opponent: ratings[o], Score: result[p]})
			}
			nr := calc.Rate(ratings[p], outcomes)
			next[p] = nr
			out[p] = calc.Ordinal(nr)
		}
		for p, r := range next {
			ratings[p] = r
		}
		return out
	}
	if err := ApplyResult(store, rater, []string{"a", "b"}, map[string]float64{"a": 1, "b": 0}); err != nil {
		t.Fatal(err)
	}
	oa, _ := store.Get("a")
	ob, _ := store.Get("b")
	if oa <= ob {
		t.Fatalf("winner ordinal should be higher: a=%v b=%v", oa, ob)
	}
}

func TestApplyResult_NilArgs(t *testing.T) {
	if err := ApplyResult(nil, func([]string, map[string]float64) map[string]float64 { return nil }, nil, nil); err == nil {
		t.Fatal("want error for nil store")
	}
	if err := ApplyResult(NewMemoryStore(), nil, nil, nil); err == nil {
		t.Fatal("want error for nil rater")
	}
}

func TestRatingHandler_HydratesSkill(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Set("u1", 1800)
	inner := func(ctx context.Context, m Match) error {
		if len(m.Tickets) != 1 {
			t.Errorf("tickets=%d", len(m.Tickets))
		}
		return nil
	}
	h := RatingHandler(inner, store, nil)
	tk := &Ticket{
		Presence:   Presence{UserID: "u1"},
		Properties: Properties{Numeric: map[string]float64{AttrSkill: 1000}},
	}
	if err := h(context.Background(), Match{Tickets: []*Ticket{tk}}); err != nil {
		t.Fatal(err)
	}
	if tk.Properties.Numeric[AttrSkill] != 1800 {
		t.Fatalf("skill=%v want 1800", tk.Properties.Numeric[AttrSkill])
	}
}

func TestMatchmaker_LatencyRejectsFarPing(t *testing.T) {
	var matches atomic.Int32
	m := New(func(ctx context.Context, mm Match) error {
		matches.Add(1)
		return nil
	}, WithTickInterval(30*time.Millisecond),
		WithMatchFunc(MultiDimScore(LatencyScore(50, 1))))
	m.Start(context.Background())
	defer func() { m.Stop(); m.Wait() }()

	add := func(uid string, ping float64) {
		_, err := m.Add(Ticket{
			Presence:   Presence{UserID: uid},
			Properties: Properties{Numeric: map[string]float64{AttrLatency: ping}},
			MinCount:   2, MaxCount: 2,
		}, "p", "b")
		if err != nil {
			t.Fatal(err)
		}
	}
	add("u1", 20)
	add("u2", 200) // 差 180ms > 50,不应匹配
	time.Sleep(150 * time.Millisecond)
	if matches.Load() != 0 {
		t.Fatalf("unexpected match count=%d", matches.Load())
	}
	if m.Count() != 2 {
		t.Fatalf("count=%d want 2 still waiting", m.Count())
	}
}

func TestMatchmaker_MatchFuncPairsClosest(t *testing.T) {
	var mu atomic.Pointer[[]string]
	m := New(func(ctx context.Context, mm Match) error {
		ids := make([]string, len(mm.Tickets))
		for i, tk := range mm.Tickets {
			ids[i] = tk.Presence.UserID
		}
		mu.Store(&ids)
		return nil
	}, WithTickInterval(30*time.Millisecond),
		WithMatchFunc(MultiDimScore(SkillScore(50, 1))))
	m.Start(context.Background())
	defer func() { m.Stop(); m.Wait() }()

	for _, p := range []struct {
		uid   string
		skill float64
	}{{"low", 100}, {"mid", 110}, {"high", 400}} {
		_, err := m.Add(Ticket{
			Presence:   Presence{UserID: p.uid},
			Properties: Properties{Numeric: map[string]float64{AttrSkill: p.skill}},
			MinCount:   2, MaxCount: 2,
		}, "p", "b")
		if err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.After(2 * time.Second)
	for mu.Load() == nil {
		select {
		case <-deadline:
			t.Fatal("no match")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	ids := *mu.Load()
	set := map[string]bool{ids[0]: true, ids[1]: true}
	if !set["low"] || !set["mid"] {
		t.Fatalf("want low+mid paired, got %v", ids)
	}
}

func TestMatchmaker_GeoMatch(t *testing.T) {
	var matches atomic.Int32
	m := New(func(ctx context.Context, mm Match) error {
		matches.Add(1)
		return nil
	}, WithTickInterval(30*time.Millisecond),
		WithMatchFunc(MultiDimScore(GeoScore(50, 1))))
	m.Start(context.Background())
	defer func() { m.Stop(); m.Wait() }()

	add := func(uid string, lat, lng float64) {
		_, err := m.Add(Ticket{
			Presence:   Presence{UserID: uid},
			Properties: Properties{Numeric: map[string]float64{AttrLat: lat, AttrLng: lng}},
			MinCount:   2, MaxCount: 2,
		}, "p", "b")
		if err != nil {
			t.Fatal(err)
		}
	}
	add("bj1", 39.90, 116.40)
	add("bj2", 39.91, 116.41)
	deadline := time.After(2 * time.Second)
	for matches.Load() < 1 {
		select {
		case <-deadline:
			t.Fatal("nearby players should match")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}
