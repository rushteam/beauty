package voxel

import (
	"sync"
	"testing"
)

// TestChunk_ConcurrentSetGet 并发读写同一 Chunk,验证 -race 安全。
func TestChunk_ConcurrentSetGet(t *testing.T) {
	c := NewChunk(ChunkPos{X: 0, Y: 0, Z: 0})
	var wg sync.WaitGroup
	const goroutines = 8
	const ops = 1000

	// 写 goroutine
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				x := (id*ops + i) % ChunkSize
				y := (id*ops + i) / ChunkSize % ChunkSize
				z := (id*ops + i) / (ChunkSize * ChunkSize) % ChunkSize
				c.Set(x, y, z, BlockID(id+1))
			}
		}(g)
	}

	// 读 goroutine
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				c.Get(i%ChunkSize, (i/ChunkSize)%ChunkSize, 0)
				c.Revision()
				c.NonAirCount()
				c.IsEmpty()
			}
		}()
	}

	wg.Wait()
}

// TestWorld_ConcurrentOps 并发加载/卸载/读写区块,验证 -race 安全。
func TestWorld_ConcurrentOps(t *testing.T) {
	w := NewWorld()
	var wg sync.WaitGroup
	const goroutines = 8
	const ops = 200

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				pos := ChunkPos{X: int32(id), Y: 0, Z: int32(i % 4)}
				switch i % 5 {
				case 0, 1:
					w.LoadChunk(pos)
				case 2:
					w.SetBlock(BlockPos{X: int32(id * ChunkSize), Y: 0, Z: int32(i)}, BlockID(i))
				case 3:
					w.GetBlock(BlockPos{X: int32(id * ChunkSize), Y: 0, Z: int32(i)})
				case 4:
					w.UnloadChunk(pos)
				}
			}
		}(g)
	}

	// 并发读
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				w.ChunkCount()
				w.LoadedChunks()
				w.HasChunk(ChunkPos{X: 0, Y: 0, Z: 0})
			}
		}()
	}

	wg.Wait()
}

// TestMutation_ConcurrentRecordCommit 并发 Record + Commit,验证 -race 安全。
func TestMutation_ConcurrentRecordCommit(t *testing.T) {
	m := NewMutation()
	w := NewWorld()
	var wg sync.WaitGroup
	const goroutines = 4
	const ops = 500

	// 并发 RecordSet
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				m.RecordSet(w, BlockPos{X: int32(id), Y: int32(i % ChunkSize), Z: 0}, BlockID(i%10+1))
			}
		}(g)
	}

	// 并发 Commit + Revision 读取
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				m.Commit()
				m.Revision()
				m.Pending()
			}
		}()
	}

	wg.Wait()
}

// TestMutationLog_ConcurrentAppendSince 并发 Append + Since,验证 -race 安全。
func TestMutationLog_ConcurrentAppendSince(t *testing.T) {
	log := NewMutationLog(32)
	var wg sync.WaitGroup
	const goroutines = 4
	const ops = 500

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				rev := uint64(id*ops + i + 1)
				log.Append(CommittedMutation{
					Revision: rev,
					Changes:  []BlockChange{{Pos: BlockPos{X: int32(i)}}},
				})
			}
		}(g)
	}

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				log.Since(uint64(i))
				log.Latest()
				log.Len()
			}
		}()
	}

	wg.Wait()
}

// TestChunk_BlocksSnapshot 验证 Blocks() 返回值拷贝不影响内部。
func TestChunk_BlocksSnapshot(t *testing.T) {
	c := NewChunk(ChunkPos{})
	c.Set(0, 0, 0, 42)
	snap := c.Blocks()

	// 修改快照不影响 Chunk
	snap[0] = 999
	if c.Get(0, 0, 0) != 42 {
		t.Error("modifying Blocks() snapshot should not affect chunk")
	}
}

// TestSnapshotRing_Concurrent 并发 Push + Latest + At,验证 -race 安全。
func TestSnapshotRing_Concurrent(t *testing.T) {
	r := NewSnapshotRing(8)
	var wg sync.WaitGroup
	const goroutines = 4
	const ops = 200

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				r.Push(WorldSnapshot{Revision: uint64(id*ops + i)})
			}
		}(g)
	}

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				r.Latest()
				r.At(uint64(i))
				r.Nearest(uint64(i))
				r.Len()
			}
		}()
	}

	wg.Wait()
}
