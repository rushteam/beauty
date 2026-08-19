package rollback_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/rollback"
)

// 简单的 1D 位移模拟:状态就是位置(int),输入是速度(int)。
type pos = int
type vel = int

var sim = rollback.SimulatorFunc[pos, vel](func(state pos, _ uint64, inputs []vel) pos {
	for _, v := range inputs {
		state += v
	}
	return state
})

func TestAdvance_LinearPrediction(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim)
	// 每帧向右移 1 格
	for i := 1; i <= 5; i++ {
		f, st := s.Advance([]vel{1})
		if f != uint64(i) {
			t.Fatalf("frame = %d, want %d", f, i)
		}
		if st != i {
			t.Fatalf("state = %d, want %d", st, i)
		}
	}
	if s.PredictionGap() != 5 {
		t.Fatalf("gap = %d", s.PredictionGap())
	}
}

func TestConfirm_PredictionCorrect_ResimSameResult(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim)
	s.Advance([]vel{1}) // f1: 0→1
	s.Advance([]vel{1}) // f2: 1→2
	s.Advance([]vel{1}) // f3: 2→3

	// 服务器确认 f1 状态=1(与预测一致),仍会重演算以保证权威覆盖
	rolled, resim, st := s.Confirm(1, 1, []vel{1})
	if !rolled {
		t.Fatal("should resim even when prediction matches")
	}
	if resim != 2 {
		t.Fatalf("resim = %d, want 2 (f2+f3)", resim)
	}
	// 因为输入一致,重演算后状态不变
	if st != 3 {
		t.Fatalf("state = %d, want 3", st)
	}
}

func TestConfirm_MispredictionTriggersRollback(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim)
	s.Advance([]vel{1}) // f1: 预测 0→1
	s.Advance([]vel{1}) // f2: 预测 1→2
	s.Advance([]vel{1}) // f3: 预测 2→3

	// 服务器说 f1 的真实状态是 10(有人被弹飞了)
	rolled, resim, st := s.Confirm(1, 10, []vel{10})
	if !rolled {
		t.Fatal("should rollback")
	}
	if resim != 2 {
		t.Fatalf("resim = %d, want 2 (f2+f3)", resim)
	}
	// 重放:f2 用本地预测输入 +1 → 11,f3 +1 → 12
	if st != 12 {
		t.Fatalf("state = %d, want 12", st)
	}
	if s.Frame() != 3 {
		t.Fatalf("frame = %d", s.Frame())
	}
}

func TestConfirm_ForcedReset_WhenGapTooLarge(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim, rollback.WithMaxRollback(2))
	for range 5 {
		s.Advance([]vel{1})
	}
	// 确认 f1,gap=4 > maxRollback=2 → 强制跳帧
	rolled, resim, st := s.Confirm(1, 100, []vel{100})
	if !rolled {
		t.Fatal("should flag as rolled")
	}
	if resim != 0 {
		t.Fatalf("forced reset should have 0 resim, got %d", resim)
	}
	if st != 100 {
		t.Fatalf("state = %d, want 100", st)
	}
	// 当前帧被重置为确认帧
	if s.Frame() != 1 {
		t.Fatalf("frame = %d", s.Frame())
	}
	stats := s.Stats()
	if stats.ForcedResets != 1 {
		t.Fatalf("ForcedResets = %d", stats.ForcedResets)
	}
}

func TestConfirm_ServerAheadOfLocal(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim)
	s.Advance([]vel{1}) // f1

	// 服务器确认的帧号 >= 当前帧:直接采纳
	rolled, _, st := s.Confirm(1, 50, []vel{50})
	if rolled {
		t.Fatal("server at same frame, no rollback")
	}
	if st != 50 {
		t.Fatalf("state = %d, want 50", st)
	}
}

func TestConfirm_IgnoresOldFrame(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim)
	s.Advance([]vel{1})
	s.Advance([]vel{1})
	s.Confirm(2, 2, []vel{1})

	// 旧帧确认被忽略
	rolled, _, _ := s.Confirm(1, 999, []vel{999})
	if rolled {
		t.Fatal("old confirm should be ignored")
	}
}

func TestSnapshotAt(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim)
	s.Advance([]vel{5}) // f1: 5
	s.Advance([]vel{3}) // f2: 8

	snap, ok := s.SnapshotAt(1)
	if !ok || snap != 5 {
		t.Fatalf("SnapshotAt(1) = %d, ok=%v", snap, ok)
	}
	snap, ok = s.SnapshotAt(2)
	if !ok || snap != 8 {
		t.Fatalf("SnapshotAt(2) = %d, ok=%v", snap, ok)
	}
}

func TestStats(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim)
	for range 4 {
		s.Advance([]vel{1})
	}
	s.Confirm(1, 10, []vel{10})
	s.Advance([]vel{1})
	s.Confirm(2, 11, []vel{1})

	stats := s.Stats()
	if stats.Rollbacks < 1 {
		t.Fatalf("should have rollbacks: %+v", stats)
	}
	if stats.TotalResim < 1 {
		t.Fatalf("should have resim: %+v", stats)
	}
}

func TestMultipleRollbacks(t *testing.T) {
	s := rollback.NewSession[pos, vel](0, sim)
	// 预测 3 帧
	s.Advance([]vel{1}) // f1: 1
	s.Advance([]vel{1}) // f2: 2
	s.Advance([]vel{1}) // f3: 3

	// 第一次纠正:f1 实际是 5
	s.Confirm(1, 5, []vel{5})
	// 重放后:f2=6, f3=7
	if s.State() != 7 {
		t.Fatalf("after 1st rollback: %d, want 7", s.State())
	}

	// 继续预测
	s.Advance([]vel{1}) // f4: 8

	// 第二次纠正:f2 实际是 20
	_, resim, st := s.Confirm(2, 20, []vel{15})
	if resim != 2 {
		t.Fatalf("2nd rollback resim = %d, want 2", resim)
	}
	// 重放:f3 用之前的输入 +1 → 21,f4 +1 → 22
	if st != 22 {
		t.Fatalf("after 2nd rollback: %d, want 22", st)
	}
}

func BenchmarkAdvance(b *testing.B) {
	s := rollback.NewSession[pos, vel](0, sim)
	for range b.N {
		s.Advance([]vel{1})
	}
}

func BenchmarkConfirmWithRollback(b *testing.B) {
	for range b.N {
		b.StopTimer()
		s := rollback.NewSession[pos, vel](0, sim)
		for range 8 {
			s.Advance([]vel{1})
		}
		b.StartTimer()
		s.Confirm(1, 100, []vel{100})
	}
}
