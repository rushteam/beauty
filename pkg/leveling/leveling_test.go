package leveling_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/leveling"
)

func TestLinear_LevelAt(t *testing.T) {
	// 每级 100 经验,满级 10。
	lv := leveling.New(leveling.Linear(100, 10))
	cases := []struct {
		exp   int64
		level int
	}{
		{0, 1}, {50, 1}, {100, 2}, {150, 2}, {250, 3}, {900, 10}, {99999, 10},
	}
	for _, c := range cases {
		if got := lv.LevelAt(c.exp); got != c.level {
			t.Errorf("LevelAt(%d) = %d, want %d", c.exp, got, c.level)
		}
	}
}

func TestGain_LevelUp(t *testing.T) {
	lv := leveling.New(leveling.Linear(100, 10))
	// 从 50 经验(1级)加 60 → 110 经验(2级),升 1 级。
	r := lv.Gain(50, 60)
	if r.Level != 2 || !r.LeveledUp || r.LevelsGain != 1 {
		t.Fatalf("gain: %+v", r)
	}
	if r.TotalExp != 110 || r.CurExp != 10 || r.NextExp != 100 {
		t.Fatalf("gain progress: total=%d cur=%d next=%d", r.TotalExp, r.CurExp, r.NextExp)
	}
}

func TestGain_MultiLevel(t *testing.T) {
	lv := leveling.New(leveling.Linear(100, 10))
	// 从 0 一次加 350 → 4 级(300),升 3 级。
	r := lv.Gain(0, 350)
	if r.Level != 4 || r.LevelsGain != 3 {
		t.Fatalf("multi-level: %+v", r)
	}
	if r.CurExp != 50 {
		t.Fatalf("curExp = %d, want 50", r.CurExp)
	}
}

func TestGain_NoLevelUp(t *testing.T) {
	lv := leveling.New(leveling.Linear(100, 10))
	r := lv.Gain(10, 20) // 30,仍 1 级
	if r.LeveledUp || r.LevelsGain != 0 {
		t.Fatalf("should not level up: %+v", r)
	}
}

func TestGain_MaxLevelOverflow(t *testing.T) {
	lv := leveling.New(leveling.Linear(100, 5)) // 满级 5 = 400 累计
	r := lv.Gain(400, 1000)
	if !r.IsMax || r.Level != 5 {
		t.Fatalf("should be max: %+v", r)
	}
	if r.NextExp != 0 {
		t.Fatalf("max level NextExp should be 0, got %d", r.NextExp)
	}
	// 满级后经验仍累计。
	if r.TotalExp != 1400 {
		t.Fatalf("total = %d, want 1400", r.TotalExp)
	}
}

func TestGain_NegativeDelta(t *testing.T) {
	lv := leveling.New(leveling.Linear(100, 10))
	r := lv.Gain(50, -100) // 夹到 0
	if r.TotalExp != 0 || r.Level != 1 {
		t.Fatalf("negative: %+v", r)
	}
	// 降级不算 LeveledUp。
	r2 := lv.Gain(250, -200) // 3级→1级(50)
	if r2.LeveledUp || r2.LevelsGain != 0 {
		t.Fatalf("downgrade should not count as levelup: %+v", r2)
	}
	if r2.Level != 1 {
		t.Fatalf("after downgrade level = %d, want 1", r2.Level)
	}
}

func TestTable(t *testing.T) {
	// 策划表:1级0, 2级50, 3级200, 4级500。
	lv := leveling.New(leveling.Table([]int64{0, 50, 200, 500}))
	if lv.Curve().MaxLevel() != 4 {
		t.Fatalf("maxLevel = %d, want 4", lv.Curve().MaxLevel())
	}
	if lv.LevelAt(199) != 2 || lv.LevelAt(200) != 3 {
		t.Fatal("table boundary wrong")
	}
	r := lv.Gain(0, 200)
	if r.Level != 3 || r.LevelsGain != 2 {
		t.Fatalf("table gain: %+v", r)
	}
}

func TestPoly(t *testing.T) {
	// 二次曲线:达到 level 级需 100*(level-1)^2。
	// 2级=100, 3级=400, 4级=900。
	lv := leveling.New(leveling.Poly(100, 2, 10))
	if lv.LevelAt(99) != 1 || lv.LevelAt(100) != 2 || lv.LevelAt(400) != 3 {
		t.Fatalf("poly levels: L(99)=%d L(100)=%d L(400)=%d",
			lv.LevelAt(99), lv.LevelAt(100), lv.LevelAt(400))
	}
}

func TestStat(t *testing.T) {
	lv := leveling.New(leveling.Linear(100, 10))
	s := lv.Stat(150) // 2级,级内50,距升级还差50
	if s.Level != 2 || s.CurExp != 50 || s.NextExp != 100 || s.LeveledUp {
		t.Fatalf("stat: %+v", s)
	}
}

func TestStat_NegativeClamped(t *testing.T) {
	lv := leveling.New(leveling.Linear(100, 10))
	if s := lv.Stat(-5); s.Level != 1 || s.TotalExp != 0 {
		t.Fatalf("negative stat: %+v", s)
	}
}
