package voxel

import "testing"

func TestChunk_SetGet(t *testing.T) {
	c := NewChunk(ChunkPos{0, 0, 0})

	// 默认全是 Air
	if got := c.Get(0, 0, 0); got != Air {
		t.Errorf("default = %d, want Air", got)
	}

	// 设置方块
	old := c.Set(5, 10, 3, 42)
	if old != Air {
		t.Errorf("old = %d, want Air", old)
	}
	if got := c.Get(5, 10, 3); got != 42 {
		t.Errorf("Get = %d, want 42", got)
	}

	// 替换方块
	old = c.Set(5, 10, 3, 99)
	if old != 42 {
		t.Errorf("old = %d, want 42", old)
	}

	// 越界
	if got := c.Get(-1, 0, 0); got != Air {
		t.Errorf("out of bounds Get = %d, want Air", got)
	}
	old = c.Set(-1, 0, 0, 1)
	if old != Air {
		t.Errorf("out of bounds Set = %d, want Air", old)
	}
}

func TestChunk_Revision(t *testing.T) {
	c := NewChunk(ChunkPos{})
	if c.Revision() != 0 {
		t.Error("initial revision should be 0")
	}
	c.Set(0, 0, 0, 1)
	if c.Revision() != 1 {
		t.Errorf("revision = %d, want 1", c.Revision())
	}
	// 相同值不递增
	c.Set(0, 0, 0, 1)
	if c.Revision() != 1 {
		t.Errorf("same value revision = %d, want 1", c.Revision())
	}
}

func TestChunk_NonAirCount(t *testing.T) {
	c := NewChunk(ChunkPos{})
	if c.NonAirCount() != 0 {
		t.Error("empty chunk should have 0 non-air")
	}
	if !c.IsEmpty() {
		t.Error("empty chunk should be empty")
	}

	c.Set(0, 0, 0, 1)
	c.Set(1, 0, 0, 2)
	if c.NonAirCount() != 2 {
		t.Errorf("NonAirCount = %d, want 2", c.NonAirCount())
	}

	c.Set(0, 0, 0, Air)
	if c.NonAirCount() != 1 {
		t.Errorf("after remove, NonAirCount = %d, want 1", c.NonAirCount())
	}
}

func TestChunk_Fill(t *testing.T) {
	c := NewChunk(ChunkPos{})
	c.Fill(7)
	if c.NonAirCount() != blocksPerChunk {
		t.Errorf("Fill: NonAirCount = %d, want %d", c.NonAirCount(), blocksPerChunk)
	}
	if c.Get(8, 8, 8) != 7 {
		t.Errorf("Fill: Get = %d, want 7", c.Get(8, 8, 8))
	}

	c.Fill(Air)
	if !c.IsEmpty() {
		t.Error("Fill(Air): should be empty")
	}
}

func TestChunk_Blocks_LoadBlocks(t *testing.T) {
	c := NewChunk(ChunkPos{})
	c.Set(0, 0, 0, 1)
	c.Set(15, 15, 15, 2)
	blocks := c.Blocks()

	c2 := NewChunk(ChunkPos{1, 0, 0})
	if !c2.LoadBlocks(blocks[:]) {
		t.Error("LoadBlocks should return true")
	}
	if c2.Get(0, 0, 0) != 1 || c2.Get(15, 15, 15) != 2 {
		t.Error("LoadBlocks data mismatch")
	}
	if c2.NonAirCount() != 2 {
		t.Errorf("LoadBlocks NonAirCount = %d, want 2", c2.NonAirCount())
	}

	// 长度不匹配
	if c2.LoadBlocks([]BlockID{1, 2, 3}) {
		t.Error("LoadBlocks should return false for wrong length")
	}
}

func TestChunk_ForEach(t *testing.T) {
	c := NewChunk(ChunkPos{})
	c.Set(3, 4, 5, 10)
	c.Set(7, 8, 9, 20)

	var count int
	c.ForEach(func(x, y, z int, block BlockID) {
		count++
	})
	if count != 2 {
		t.Errorf("ForEach count = %d, want 2", count)
	}
}

func TestIndex_Deindex_Roundtrip(t *testing.T) {
	for y := 0; y < ChunkSize; y++ {
		for z := 0; z < ChunkSize; z++ {
			for x := 0; x < ChunkSize; x++ {
				idx := index(x, y, z)
				gx, gy, gz := deindex(idx)
				if gx != x || gy != y || gz != z {
					t.Fatalf("index/deindex mismatch: (%d,%d,%d) -> %d -> (%d,%d,%d)",
						x, y, z, idx, gx, gy, gz)
				}
			}
		}
	}
}
