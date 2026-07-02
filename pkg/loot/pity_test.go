package loot_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/loot"
)

// 稀有物品权重极低,靠保底兜底。
func pityTable() *loot.Table[string] {
	items := []loot.Item[string]{
		{Value: "common", Weight: 990, Rarity: 1},
		{Value: "5star", Weight: 1, Rarity: 5}, // 极低概率
	}
	tb, _ := loot.NewTable(items, loot.WithRand[string](newRand()))
	return tb
}

func TestPuller_PityTriggersAtLimit(t *testing.T) {
	// pityLimit=10:连续 10 次没出 Rarity>=5,第 11 抽必出。
	p := loot.NewPuller(pityTable(), 10, 5)

	var got5star, gotPity bool
	for i := range 11 {
		it, pity := p.Draw()
		if it.Rarity >= 5 {
			got5star = true
			if i == 10 && pity {
				gotPity = true // 第 11 抽(index 10)由保底触发
			}
			break
		}
	}
	if !got5star {
		t.Fatal("should get a 5star within pity limit")
	}
	if !gotPity {
		t.Fatal("the 5star at the limit should be pity-triggered")
	}
}

func TestPuller_MissResetsOnNaturalHit(t *testing.T) {
	// 用一张必出高稀有度的表,验证自然出货会重置计数。
	items := []loot.Item[string]{{Value: "5star", Weight: 1, Rarity: 5}}
	tb, _ := loot.NewTable(items, loot.WithRand[string](newRand()))
	p := loot.NewPuller(tb, 100, 5)

	it, pity := p.Draw()
	if it.Rarity != 5 || pity {
		t.Fatalf("natural hit expected, got rarity=%d pity=%v", it.Rarity, pity)
	}
	if p.Misses() != 0 {
		t.Fatalf("misses should reset to 0 on natural hit, got %d", p.Misses())
	}
}

func TestPuller_MissCounting(t *testing.T) {
	// 只含 common(永不自然出货),misses 应持续累加直到保底。
	items := []loot.Item[string]{{Value: "common", Weight: 1, Rarity: 1}}
	tb, _ := loot.NewTable(items, loot.WithRand[string](newRand()))
	p := loot.NewPuller(tb, 5, 5) // 无高稀有度子表 → 保底无法触发

	for range 3 {
		p.Draw()
	}
	if p.Misses() != 3 {
		t.Fatalf("misses = %d, want 3", p.Misses())
	}
	p.Reset()
	if p.Misses() != 0 {
		t.Fatalf("after reset misses = %d", p.Misses())
	}
}

func TestPuller_NoPityWhenDisabled(t *testing.T) {
	// pityLimit<=0 → 等价裸抽,misses 不累加。
	p := loot.NewPuller(pityTable(), 0, 5)
	for range 50 {
		p.Draw()
	}
	if p.Misses() != 0 {
		t.Fatalf("disabled pity should not count misses, got %d", p.Misses())
	}
}

func TestPuller_DrawN(t *testing.T) {
	p := loot.NewPuller(pityTable(), 10, 5)
	items, pity := p.DrawN(25)
	if len(items) != 25 || len(pity) != 25 {
		t.Fatalf("DrawN lengths: %d/%d", len(items), len(pity))
	}
	// 25 抽内保底阈值 10,至少应触发一次保底(或自然出货)。
	var hits int
	for _, it := range items {
		if it.Rarity >= 5 {
			hits++
		}
	}
	if hits == 0 {
		t.Fatal("25 draws with pity=10 should yield at least one 5star")
	}
}
