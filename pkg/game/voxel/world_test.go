package voxel

import "testing"

func TestWorld_LoadUnload(t *testing.T) {
	w := NewWorld()
	pos := ChunkPos{0, 0, 0}

	c, created := w.LoadChunk(pos)
	if !created {
		t.Error("first LoadChunk should be created")
	}
	if c == nil {
		t.Fatal("chunk should not be nil")
	}
	if w.ChunkCount() != 1 {
		t.Errorf("ChunkCount = %d, want 1", w.ChunkCount())
	}

	// 重复加载
	c2, created := w.LoadChunk(pos)
	if created {
		t.Error("second LoadChunk should not be created")
	}
	if c2 != c {
		t.Error("should return same chunk")
	}

	// 卸载
	unloaded := w.UnloadChunk(pos)
	if unloaded != c {
		t.Error("UnloadChunk should return the chunk")
	}
	if w.ChunkCount() != 0 {
		t.Errorf("after unload, ChunkCount = %d, want 0", w.ChunkCount())
	}

	// 再次卸载
	if w.UnloadChunk(pos) != nil {
		t.Error("double unload should return nil")
	}
}

func TestWorld_GetSetBlock(t *testing.T) {
	w := NewWorld()

	// 未加载区块读取
	if w.GetBlock(BlockPos{0, 0, 0}) != Air {
		t.Error("unloaded chunk should return Air")
	}

	// SetBlock 自动创建区块
	w.SetBlock(BlockPos{5, 10, 3}, 42)
	if w.GetBlock(BlockPos{5, 10, 3}) != 42 {
		t.Error("SetBlock/GetBlock mismatch")
	}
	if w.ChunkCount() != 1 {
		t.Errorf("ChunkCount = %d, want 1", w.ChunkCount())
	}

	// 跨区块
	w.SetBlock(BlockPos{16, 0, 0}, 99)
	if w.GetBlock(BlockPos{16, 0, 0}) != 99 {
		t.Error("cross-chunk SetBlock/GetBlock mismatch")
	}
	if w.ChunkCount() != 2 {
		t.Errorf("ChunkCount = %d, want 2", w.ChunkCount())
	}

	// 负坐标
	w.SetBlock(BlockPos{-1, -1, -1}, 7)
	if w.GetBlock(BlockPos{-1, -1, -1}) != 7 {
		t.Error("negative coord SetBlock/GetBlock mismatch")
	}
	cp := BlockPos{-1, -1, -1}.ToChunkPos()
	if !w.HasChunk(cp) {
		t.Errorf("chunk at %v should be loaded", cp)
	}
}

func TestWorld_ChunksInRadius(t *testing.T) {
	w := NewWorld()
	for x := int32(-2); x <= 2; x++ {
		for z := int32(-2); z <= 2; z++ {
			w.LoadChunk(ChunkPos{x, 0, z})
		}
	}

	chunks := w.ChunksInRadius(ChunkPos{0, 0, 0}, 1)
	if len(chunks) != 9 { // 3x1x3 = 9
		t.Errorf("ChunksInRadius(1) = %d, want 9", len(chunks))
	}
}

func TestWorld_ForEachChunk(t *testing.T) {
	w := NewWorld()
	w.LoadChunk(ChunkPos{0, 0, 0})
	w.LoadChunk(ChunkPos{1, 0, 0})

	var count int
	w.ForEachChunk(func(pos ChunkPos, c *Chunk) {
		count++
	})
	if count != 2 {
		t.Errorf("ForEachChunk count = %d, want 2", count)
	}
}
