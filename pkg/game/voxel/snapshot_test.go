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

func TestTakeIncrementalSnapshot(t *testing.T) {
	w := NewWorld()
	m := NewMutation()

	// 初始填充 3 个区块
	for i := int32(0); i < 3; i++ {
		c, _ := w.LoadChunk(ChunkPos{X: i, Y: 0, Z: 0})
		c.Fill(BlockID(i + 1))
	}

	// 全量快照作为基线
	base := TakeSnapshot(w, nil)
	if len(base.Chunks) != 3 {
		t.Fatalf("base chunks = %d, want 3", len(base.Chunks))
	}

	// 清除脏标记(模拟快照后清理)
	w.ForEachChunk(func(_ ChunkPos, c *Chunk) {
		c.markClean()
	})

	// 无修改 → 增量快照应为空
	incr := TakeIncrementalSnapshot(w, nil)
	if len(incr.Chunks) != 0 {
		t.Errorf("no-dirty incremental chunks = %d, want 0", len(incr.Chunks))
	}

	// 修改 1 个区块
	m.RecordSet(w, BlockPos{X: 0, Y: 0, Z: 0}, 99)
	cm := m.Commit()
	_ = cm

	// 增量快照应只含 1 个区块
	incr = TakeIncrementalSnapshot(w, m)
	if len(incr.Chunks) != 1 {
		t.Errorf("1-dirty incremental chunks = %d, want 1", len(incr.Chunks))
	}
	if incr.Chunks[0].Pos != (ChunkPos{X: 0, Y: 0, Z: 0}) {
		t.Errorf("dirty chunk pos = %v, want {0,0,0}", incr.Chunks[0].Pos)
	}

	// 脏标记应已清除
	c := w.Chunk(ChunkPos{X: 0, Y: 0, Z: 0})
	if c.IsDirty() {
		t.Error("chunk should be clean after incremental snapshot")
	}
}

func TestMergeSnapshot(t *testing.T) {
	w := NewWorld()

	// 建立 3 个区块
	for i := int32(0); i < 3; i++ {
		c, _ := w.LoadChunk(ChunkPos{X: i, Y: 0, Z: 0})
		c.Fill(BlockID(i + 1))
	}

	// 全量基线
	base := TakeSnapshot(w, nil)
	w.ForEachChunk(func(_ ChunkPos, c *Chunk) { c.markClean() })

	// 修改区块 0 并增加新区块 3
	w.SetBlock(BlockPos{X: 0, Y: 0, Z: 0}, 99)
	c3, _ := w.LoadChunk(ChunkPos{X: 3, Y: 0, Z: 0})
	c3.Fill(10)

	delta := TakeIncrementalSnapshot(w, nil)
	if len(delta.Chunks) != 2 {
		t.Fatalf("delta chunks = %d, want 2", len(delta.Chunks))
	}

	// 合并
	merged := MergeSnapshot(base, delta)
	if len(merged.Chunks) != 4 {
		t.Errorf("merged chunks = %d, want 4", len(merged.Chunks))
	}

	// 恢复验证
	w2 := NewWorld()
	RestoreSnapshot(w2, merged)
	if w2.GetBlock(BlockPos{X: 0, Y: 0, Z: 0}) != 99 {
		t.Error("merged block at (0,0,0) should be 99")
	}
	if w2.GetBlock(BlockPos{X: ChunkSize, Y: 0, Z: 0}) != 2 {
		t.Error("unmodified block at chunk 1 should be 2")
	}
	if w2.ChunkCount() != 4 {
		t.Errorf("restored ChunkCount = %d, want 4", w2.ChunkCount())
	}
}
