package loot_test

import (
	"math/rand/v2"
	"testing"

	"github.com/rushteam/beauty/pkg/loot"
)

func newRand() *rand.Rand {
	return rand.New(rand.NewPCG(42, 1024)) // 固定种子,可复现
}

func TestNewTable_Empty(t *testing.T) {
	if _, err := loot.NewTable[string](nil); err != loot.ErrEmptyTable {
		t.Fatalf("empty items: err = %v, want ErrEmptyTable", err)
	}
	// 全部权重 <=0 也视为空。
	items := []loot.Item[string]{{Value: "a", Weight: 0}, {Value: "b", Weight: -1}}
	if _, err := loot.NewTable(items); err != loot.ErrEmptyTable {
		t.Fatalf("all non-positive: err = %v, want ErrEmptyTable", err)
	}
}

func TestDraw_Distribution(t *testing.T) {
	// 权重 1:2:7,抽 100k 次,频率应接近 10%:20%:70%。
	items := []loot.Item[string]{
		{Value: "common", Weight: 7},
		{Value: "rare", Weight: 2},
		{Value: "epic", Weight: 1},
	}
	tb, err := loot.NewTable(items, loot.WithRand[string](newRand()))
	if err != nil {
		t.Fatal(err)
	}
	const n = 100000
	count := map[string]int{}
	for range n {
		count[tb.Draw()]++
	}
	check := func(name string, wantFrac float64) {
		got := float64(count[name]) / n
		if got < wantFrac-0.02 || got > wantFrac+0.02 {
			t.Errorf("%s frequency = %.3f, want ~%.3f", name, got, wantFrac)
		}
	}
	check("common", 0.7)
	check("rare", 0.2)
	check("epic", 0.1)
}

func TestDraw_SingleItem(t *testing.T) {
	tb, _ := loot.NewTable([]loot.Item[int]{{Value: 99, Weight: 5}}, loot.WithRand[int](newRand()))
	for range 100 {
		if tb.Draw() != 99 {
			t.Fatal("single-item table must always return that item")
		}
	}
}

func TestDrawN(t *testing.T) {
	tb, _ := loot.NewTable([]loot.Item[int]{{Value: 1, Weight: 1}, {Value: 2, Weight: 1}}, loot.WithRand[int](newRand()))
	got := tb.DrawN(10)
	if len(got) != 10 {
		t.Fatalf("DrawN(10) len = %d", len(got))
	}
	if tb.DrawN(0) != nil {
		t.Fatal("DrawN(0) should be nil")
	}
}

func TestDrawDistinct(t *testing.T) {
	items := []loot.Item[int]{
		{Value: 1, Weight: 1}, {Value: 2, Weight: 1}, {Value: 3, Weight: 1},
		{Value: 4, Weight: 1}, {Value: 5, Weight: 1},
	}
	tb, _ := loot.NewTable(items, loot.WithRand[int](newRand()))
	got := tb.DrawDistinct(3)
	if len(got) != 3 {
		t.Fatalf("distinct len = %d, want 3", len(got))
	}
	seen := map[int]bool{}
	for _, v := range got {
		if seen[v] {
			t.Fatalf("DrawDistinct returned duplicate: %v", got)
		}
		seen[v] = true
	}
}

func TestDrawDistinct_MoreThanItems(t *testing.T) {
	items := []loot.Item[int]{{Value: 1, Weight: 1}, {Value: 2, Weight: 1}}
	tb, _ := loot.NewTable(items, loot.WithRand[int](newRand()))
	got := tb.DrawDistinct(10) // 只有 2 个物品
	if len(got) != 2 {
		t.Fatalf("distinct capped len = %d, want 2", len(got))
	}
}

func TestWeightsIgnoreNonPositive(t *testing.T) {
	items := []loot.Item[string]{
		{Value: "a", Weight: 5},
		{Value: "skip", Weight: 0},
		{Value: "b", Weight: 5},
	}
	tb, _ := loot.NewTable(items, loot.WithRand[string](newRand()))
	if tb.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (zero-weight ignored)", tb.Len())
	}
	for range 1000 {
		if tb.Draw() == "skip" {
			t.Fatal("zero-weight item should never be drawn")
		}
	}
}

func TestConcurrentDraw(t *testing.T) {
	// Table 用全局 rand(并发安全),并发抽取不应 race。
	items := []loot.Item[int]{{Value: 1, Weight: 3}, {Value: 2, Weight: 1}}
	tb, _ := loot.NewTable(items) // 不带 WithRand → 全局源
	done := make(chan struct{}, 20)
	for range 20 {
		go func() {
			for range 10000 {
				_ = tb.Draw()
			}
			done <- struct{}{}
		}()
	}
	for range 20 {
		<-done
	}
}
