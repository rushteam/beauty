package voxel

import "testing"

func TestTakeRestoreSnapshot(t *testing.T) {
	w := NewWorld()
	m := NewMutation()

	m.RecordSet(w, BlockPos{5, 5, 5}, 42)
	m.RecordSet(w, BlockPos{20, 0, 0}, 7)
	m.Commit()

	snap := TakeSnapshot(w, m)
	if snap.Revision != 1 {
		t.Errorf("snap Revision = %d, want 1", snap.Revision)
	}
	if len(snap.Chunks) != 2 {
		t.Errorf("snap Chunks = %d, want 2", len(snap.Chunks))
	}

	// 恢复到新 World
	w2 := NewWorld()
	RestoreSnapshot(w2, snap)
	if w2.ChunkCount() != 2 {
		t.Errorf("restored ChunkCount = %d, want 2", w2.ChunkCount())
	}
	if w2.GetBlock(BlockPos{5, 5, 5}) != 42 {
		t.Error("restored block mismatch")
	}
	if w2.GetBlock(BlockPos{20, 0, 0}) != 7 {
		t.Error("restored block mismatch 2")
	}
}

func TestTakeSnapshot_NoMutation(t *testing.T) {
	w := NewWorld()
	c, _ := w.LoadChunk(ChunkPos{0, 0, 0})
	c.Set(1, 1, 1, 5)

	snap := TakeSnapshot(w, nil)
	if snap.Revision != c.Revision() {
		t.Errorf("snap Revision = %d, want %d", snap.Revision, c.Revision())
	}
}

func TestSnapshotRing(t *testing.T) {
	r := NewSnapshotRing(3)
	if r.Len() != 0 {
		t.Error("empty ring Len should be 0")
	}
	if _, ok := r.Latest(); ok {
		t.Error("empty ring Latest should be false")
	}

	for i := uint64(1); i <= 5; i++ {
		r.Push(WorldSnapshot{Revision: i})
	}

	// 最新
	snap, ok := r.Latest()
	if !ok || snap.Revision != 5 {
		t.Errorf("Latest = %d, want 5", snap.Revision)
	}

	// 精确查找
	snap, ok = r.At(4)
	if !ok || snap.Revision != 4 {
		t.Errorf("At(4) = %d, want 4", snap.Revision)
	}
	_, ok = r.At(1) // 已被覆盖(depth=3)
	if ok {
		t.Error("At(1) should be false (overwritten)")
	}

	// Nearest
	snap, rev, ok := r.Nearest(4)
	if !ok || rev != 4 {
		t.Errorf("Nearest(4) rev = %d, want 4", rev)
	}
	_ = snap

	_, _, ok = r.Nearest(2) // 2 已覆盖,最小保留的是 3,但 3 > 2 所以找不到
	if ok {
		t.Error("Nearest(2) should be false (no snapshot <= 2)")
	}
}
