package voxel

import (
	"sync"
	"sync/atomic"
)

const (
	// worldShardBits 分片锁位数。2^5 = 32 个分片,
	// 在大型多人游戏中,不同区块的并发读写分散到不同分片,大幅降低锁竞争。
	worldShardBits  = 5
	worldShardCount = 1 << worldShardBits // 32
)

// worldShard 是 World 内部的锁分片,持有该分片对应的区块子集。
type worldShard struct {
	mu     sync.RWMutex
	chunks map[ChunkPos]*Chunk
}

// World 管理多个 Chunk 的体素世界。
//
// 性能特性(v0.9.3+):
//   - 分片锁: 内部 32 个分片替代单一全局锁,并发读写争用降低 ~30 倍
//   - 原子计数: ChunkCount() 为 O(1) 原子读取
//   - 自适应范围查询: ChunksInRadius 根据半径/总量自动选择最优策略
//
// 支持按需加载/卸载区块、跨区块方块读写、范围查询。
// 并发安全。零值不可用,用 NewWorld 构造。
type World struct {
	shards [worldShardCount]worldShard
	count  atomic.Int64 // 已加载区块总数,O(1) 查询
}

// NewWorld 创建空世界。
func NewWorld() *World {
	w := &World{}
	for i := range w.shards {
		w.shards[i].chunks = make(map[ChunkPos]*Chunk)
	}
	return w
}

// shardFor 根据区块坐标哈希选取分片(FNV-1a 风格,空间均匀分布)。
func (w *World) shardFor(pos ChunkPos) *worldShard {
	h := uint32(2166136261)
	h ^= uint32(pos.X)
	h *= 16777619
	h ^= uint32(pos.Y)
	h *= 16777619
	h ^= uint32(pos.Z)
	h *= 16777619
	return &w.shards[h&(worldShardCount-1)]
}

// LoadChunk 加载或创建区块。如果该位置已有区块则返回现有的。
// 返回 (chunk, created),created=true 表示是新建的。
func (w *World) LoadChunk(pos ChunkPos) (c *Chunk, created bool) {
	s := w.shardFor(pos)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.chunks[pos]; ok {
		return existing, false
	}
	c = NewChunk(pos)
	s.chunks[pos] = c
	w.count.Add(1)
	return c, true
}

// UnloadChunk 卸载区块,返回被卸载的 Chunk(可用于持久化)。
// 不存在返回 nil。
func (w *World) UnloadChunk(pos ChunkPos) *Chunk {
	s := w.shardFor(pos)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chunks[pos]
	if !ok {
		return nil
	}
	delete(s.chunks, pos)
	w.count.Add(-1)
	return c
}

// Chunk 返回指定位置的区块。不存在返回 nil。
func (w *World) Chunk(pos ChunkPos) *Chunk {
	s := w.shardFor(pos)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.chunks[pos]
}

// HasChunk 检查区块是否已加载。
func (w *World) HasChunk(pos ChunkPos) bool {
	s := w.shardFor(pos)
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.chunks[pos]
	return ok
}

// ChunkCount 返回已加载的区块数。O(1) 原子读取。
func (w *World) ChunkCount() int {
	return int(w.count.Load())
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
	out := make([]ChunkPos, 0, w.ChunkCount())
	for i := range w.shards {
		s := &w.shards[i]
		s.mu.RLock()
		for pos := range s.chunks {
			out = append(out, pos)
		}
		s.mu.RUnlock()
	}
	return out
}

// ChunksInRadius 返回以 center 为中心、半径 radius(区块距离)内的所有已加载区块。
// 使用切比雪夫距离(立方体范围)。
//
// 自动选择最优策略:
//   - 小半径 + 大世界 → 枚举候选坐标逐个查表, O(radius³)
//   - 大半径 + 小世界 → 遍历所有已加载区块, O(N)
func (w *World) ChunksInRadius(center ChunkPos, radius int32) []*Chunk {
	side := int64(2*radius + 1)
	boxVolume := side * side * side
	total := w.count.Load()
	if total > 0 && boxVolume < total {
		return w.chunksInRadiusByLookup(center, radius)
	}
	return w.chunksInRadiusByScan(center, radius)
}

// chunksInRadiusByLookup 枚举立方体内坐标逐个查找。
// 适合小半径 + 大世界(视距 5 → 1331 次查表 vs 遍历 10 万区块)。
func (w *World) chunksInRadiusByLookup(center ChunkPos, radius int32) []*Chunk {
	var out []*Chunk
	for dx := -radius; dx <= radius; dx++ {
		for dy := -radius; dy <= radius; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				pos := ChunkPos{center.X + dx, center.Y + dy, center.Z + dz}
				if c := w.Chunk(pos); c != nil {
					out = append(out, c)
				}
			}
		}
	}
	return out
}

// chunksInRadiusByScan 逐分片遍历所有已加载区块过滤。
// 适合大半径或小世界。
func (w *World) chunksInRadiusByScan(center ChunkPos, radius int32) []*Chunk {
	var out []*Chunk
	for i := range w.shards {
		s := &w.shards[i]
		s.mu.RLock()
		for pos, c := range s.chunks {
			dx := abs32(pos.X - center.X)
			dy := abs32(pos.Y - center.Y)
			dz := abs32(pos.Z - center.Z)
			if dx <= radius && dy <= radius && dz <= radius {
				out = append(out, c)
			}
		}
		s.mu.RUnlock()
	}
	return out
}

// ForEachChunk 遍历所有已加载区块。逐分片加锁,不阻塞整个世界。
// 回调中可安全读取 chunk,不应调用 World 方法(避免死锁)。
func (w *World) ForEachChunk(fn func(pos ChunkPos, c *Chunk)) {
	for i := range w.shards {
		s := &w.shards[i]
		s.mu.RLock()
		for pos, c := range s.chunks {
			fn(pos, c)
		}
		s.mu.RUnlock()
	}
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
