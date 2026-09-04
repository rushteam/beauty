package voxel

import (
	"fmt"
	"testing"
)

// ---------- World 并发读写 ----------

func BenchmarkWorld_SetBlock_Parallel(b *testing.B) {
	w := NewWorld()
	for x := int32(0); x < 10; x++ {
		for z := int32(0); z < 10; z++ {
			w.LoadChunk(ChunkPos{x, 0, z})
		}
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			pos := BlockPos{int32(i % 160), 0, int32((i / 160) % 160)}
			w.SetBlock(pos, BlockID(i%255+1))
			i++
		}
	})
}

func BenchmarkWorld_GetBlock_Parallel(b *testing.B) {
	w := NewWorld()
	for x := int32(0); x < 10; x++ {
		for z := int32(0); z < 10; z++ {
			c, _ := w.LoadChunk(ChunkPos{x, 0, z})
			c.Fill(1)
		}
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			pos := BlockPos{int32(i % 160), 0, int32((i / 160) % 160)}
			w.GetBlock(pos)
			i++
		}
	})
}

// ---------- ChunksInRadius 自适应策略 ----------

func BenchmarkChunksInRadius(b *testing.B) {
	w := NewWorld()
	// 加载 101x1x101 = 10201 个区块
	for x := int32(-50); x <= 50; x++ {
		for z := int32(-50); z <= 50; z++ {
			w.LoadChunk(ChunkPos{x, 0, z})
		}
	}

	for _, radius := range []int32{3, 5, 10, 20} {
		b.Run(fmt.Sprintf("radius=%d/total=%d", radius, w.ChunkCount()), func(b *testing.B) {
			center := ChunkPos{0, 0, 0}
			for i := 0; i < b.N; i++ {
				w.ChunksInRadius(center, radius)
			}
		})
	}
}

// ---------- RLE 编码:零拷贝 vs 带拷贝 ----------

func BenchmarkEncodeChunkRLE(b *testing.B) {
	c := NewChunk(ChunkPos{})
	// 模拟地形:下半部分为石头,上半部分为空气
	for y := 0; y < ChunkSize/2; y++ {
		for x := 0; x < ChunkSize; x++ {
			for z := 0; z < ChunkSize; z++ {
				c.Set(x, y, z, 1)
			}
		}
	}

	b.Run("ZeroCopy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			c.EncodeRLE()
		}
	})
	b.Run("WithCopy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			blocks := c.Blocks()
			EncodeRLE(blocks[:])
		}
	})
}

// ---------- 快照:全量 vs 增量 ----------

func BenchmarkTakeSnapshot(b *testing.B) {
	w := NewWorld()
	// 400 个区块
	for x := int32(0); x < 20; x++ {
		for z := int32(0); z < 20; z++ {
			c, _ := w.LoadChunk(ChunkPos{x, 0, z})
			c.Fill(1)
		}
	}

	b.Run("Full_400chunks", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			TakeSnapshot(w, nil)
		}
	})

	b.Run("Incremental_NoDirty", func(b *testing.B) {
		// 清除所有脏标记
		w.ForEachChunk(func(_ ChunkPos, c *Chunk) {
			c.markClean()
		})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			TakeIncrementalSnapshot(w, nil)
		}
	})

	b.Run("Incremental_10Dirty", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// 弄脏 10 个区块
			for x := int32(0); x < 10; x++ {
				w.SetBlock(BlockPos{x * ChunkSize, 0, 0}, BlockID(i%255+1))
			}
			TakeIncrementalSnapshot(w, nil)
		}
	})
}

// ---------- Chunk 单操作 ----------

func BenchmarkChunk_Set(b *testing.B) {
	c := NewChunk(ChunkPos{})
	for i := 0; i < b.N; i++ {
		c.Set(i%ChunkSize, (i/ChunkSize)%ChunkSize, (i/(ChunkSize*ChunkSize))%ChunkSize, BlockID(i%255+1))
	}
}

func BenchmarkChunk_Get(b *testing.B) {
	c := NewChunk(ChunkPos{})
	c.Fill(1)
	for i := 0; i < b.N; i++ {
		c.Get(i%ChunkSize, (i/ChunkSize)%ChunkSize, (i/(ChunkSize*ChunkSize))%ChunkSize)
	}
}
