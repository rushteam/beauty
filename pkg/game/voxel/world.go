package voxel

import "sync"

// World 管理多个 Chunk 的体素世界。
//
// 支持按需加载/卸载区块、跨区块方块读写、范围查询。
// 并发安全(读写锁)。零值不可用,用 NewWorld 构造。
type World struct {
	mu     sync.RWMutex
	chunks map[ChunkPos]*Chunk
}

// NewWorld 创建空世界。
func NewWorld() *World {
	return &World{chunks: make(map[ChunkPos]*Chunk)}
}

// LoadChunk 加载或创建区块。如果该位置已有区块则返回现有的。
// 返回 (chunk, loaded),loaded=true 表示是新建的。
func (w *World) LoadChunk(pos ChunkPos) (c *Chunk, created bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if existing, ok := w.chunks[pos]; ok {
		return existing, false
	}
	c = NewChunk(pos)
	w.chunks[pos] = c
	return c, true
}

// UnloadChunk 卸载区块,返回被卸载的 Chunk(可用于持久化)。
// 不存在返回 nil。
func (w *World) UnloadChunk(pos ChunkPos) *Chunk {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ok := w.chunks[pos]
	if !ok {
		return nil
	}
	delete(w.chunks, pos)
	return c
}

// Chunk 返回指定位置的区块。不存在返回 nil。
func (w *World) Chunk(pos ChunkPos) *Chunk {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.chunks[pos]
}

// HasChunk 检查区块是否已加载。
func (w *World) HasChunk(pos ChunkPos) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.chunks[pos]
	return ok
}

// ChunkCount 返回已加载的区块数。
func (w *World) ChunkCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.chunks)
}

// GetBlock 读取世界绝对坐标处的方块。区块未加载时返回 Air。
func (w *World) GetBlock(pos BlockPos) BlockID {
	cp := pos.ToChunkPos()
	c := w.Chunk(cp)
	if c == nil {
		return Air
	}
	x, y, z := pos.ToLocal()
	return c.Get(x, y, z)
}

// SetBlock 设置世界绝对坐标处的方块,返回旧方块。
// 如果区块未加载,自动创建。
func (w *World) SetBlock(pos BlockPos, block BlockID) BlockID {
	cp := pos.ToChunkPos()
	c, _ := w.LoadChunk(cp)
	x, y, z := pos.ToLocal()
	return c.Set(x, y, z, block)
}

// LoadedChunks 返回所有已加载区块坐标的快照。
func (w *World) LoadedChunks() []ChunkPos {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]ChunkPos, 0, len(w.chunks))
	for pos := range w.chunks {
		out = append(out, pos)
	}
	return out
}

// ChunksInRadius 返回以 center 为中心、半径 radius(区块距离)内的所有已加载区块。
// 使用切比雪夫距离(立方体范围)。
func (w *World) ChunksInRadius(center ChunkPos, radius int32) []*Chunk {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var out []*Chunk
	for pos, c := range w.chunks {
		dx := abs32(pos.X - center.X)
		dy := abs32(pos.Y - center.Y)
		dz := abs32(pos.Z - center.Z)
		if dx <= radius && dy <= radius && dz <= radius {
			out = append(out, c)
		}
	}
	return out
}

// ForEachChunk 遍历所有已加载区块。回调中可安全读取 chunk,不应调用 World 方法(避免死锁)。
func (w *World) ForEachChunk(fn func(pos ChunkPos, c *Chunk)) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for pos, c := range w.chunks {
		fn(pos, c)
	}
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
