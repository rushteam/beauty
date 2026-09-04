package voxel

import "sync"

// Chunk 是一个 ChunkSize³ 的方块数组,是体素世界的最小加载/传输单元。
//
// 内部使用扁平数组(而非三维切片)以获得缓存友好的连续内存布局。
// 并发安全(读写锁),零值不可用,用 NewChunk 构造。
type Chunk struct {
	mu       sync.RWMutex
	pos      ChunkPos
	blocks   [blocksPerChunk]BlockID
	revision uint64
	nonAir   int // 非空气方块计数,用于快速判空
}

// NewChunk 创建位于 pos 的空区块(全部为 Air)。
func NewChunk(pos ChunkPos) *Chunk {
	return &Chunk{pos: pos}
}

// Pos 返回区块坐标。
func (c *Chunk) Pos() ChunkPos { return c.pos }

// Revision 返回当前修订号(每次 Set 递增)。
func (c *Chunk) Revision() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.revision
}

// Get 返回局部坐标 (x,y,z) 处的方块。坐标越界返回 Air。
func (c *Chunk) Get(x, y, z int) BlockID {
	if !inBounds(x, y, z) {
		return Air
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blocks[index(x, y, z)]
}

// Set 设置局部坐标 (x,y,z) 处的方块,返回被替换的旧方块。
// 坐标越界时无操作返回 Air。每次成功修改会递增 Revision。
func (c *Chunk) Set(x, y, z int, block BlockID) (old BlockID) {
	if !inBounds(x, y, z) {
		return Air
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := index(x, y, z)
	old = c.blocks[idx]
	if old == block {
		return old
	}
	c.blocks[idx] = block
	c.revision++

	switch {
	case old == Air && block != Air:
		c.nonAir++
	case old != Air && block == Air:
		c.nonAir--
	}
	return old
}

// Fill 用 block 填充整个区块。
func (c *Chunk) Fill(block BlockID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.blocks {
		c.blocks[i] = block
	}
	c.revision++
	if block == Air {
		c.nonAir = 0
	} else {
		c.nonAir = blocksPerChunk
	}
}

// IsEmpty 返回区块是否全部为 Air。
func (c *Chunk) IsEmpty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nonAir == 0
}

// NonAirCount 返回非空气方块的数量。
func (c *Chunk) NonAirCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nonAir
}

// Blocks 返回底层方块数组的拷贝(用于序列化/快照)。
func (c *Chunk) Blocks() [blocksPerChunk]BlockID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blocks
}

// LoadBlocks 从外部数据恢复方块(反序列化/快照加载)。data 长度必须等于 blocksPerChunk。
func (c *Chunk) LoadBlocks(data []BlockID) bool {
	if len(data) != blocksPerChunk {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for i, b := range data {
		c.blocks[i] = b
		if b != Air {
			count++
		}
	}
	c.nonAir = count
	c.revision++
	return true
}

// ForEach 遍历区块中所有非 Air 方块。回调参数为局部坐标和方块 ID。
func (c *Chunk) ForEach(fn func(x, y, z int, block BlockID)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i, b := range c.blocks {
		if b == Air {
			continue
		}
		x, y, z := deindex(i)
		fn(x, y, z, b)
	}
}

func inBounds(x, y, z int) bool {
	return x >= 0 && x < ChunkSize &&
		y >= 0 && y < ChunkSize &&
		z >= 0 && z < ChunkSize
}

func index(x, y, z int) int {
	return y*ChunkSize*ChunkSize + z*ChunkSize + x
}

func deindex(i int) (x, y, z int) {
	x = i % ChunkSize
	z = (i / ChunkSize) % ChunkSize
	y = i / (ChunkSize * ChunkSize)
	return
}
