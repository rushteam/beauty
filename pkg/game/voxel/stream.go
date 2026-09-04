package voxel

import "encoding/binary"

// RLERun 游程编码的单个游程:连续 Count 个 Block。
type RLERun struct {
	Block BlockID
	Count uint16
}

// EncodeRLE 将方块数组进行 Run-Length 压缩。
// 对于包含大面积相同方块的区块(常见于空气/石头/水层),压缩率通常 5~20 倍。
func EncodeRLE(blocks []BlockID) []RLERun {
	if len(blocks) == 0 {
		return nil
	}
	runs := make([]RLERun, 0, 64) // 预分配合理初始容量
	current := blocks[0]
	count := uint16(1)
	for i := 1; i < len(blocks); i++ {
		if blocks[i] == current && count < ^uint16(0) {
			count++
		} else {
			runs = append(runs, RLERun{Block: current, Count: count})
			current = blocks[i]
			count = 1
		}
	}
	runs = append(runs, RLERun{Block: current, Count: count})
	return runs
}

// DecodeRLE 将游程编码还原为方块数组。
func DecodeRLE(runs []RLERun) []BlockID {
	if len(runs) == 0 {
		return nil
	}
	total := 0
	for _, r := range runs {
		total += int(r.Count)
	}
	blocks := make([]BlockID, 0, total)
	for _, r := range runs {
		for i := uint16(0); i < r.Count; i++ {
			blocks = append(blocks, r.Block)
		}
	}
	return blocks
}

// EncodeChunkRLE 对 Chunk 的方块数据进行 RLE 压缩。
func EncodeChunkRLE(c *Chunk) []RLERun {
	b := c.Blocks()
	return EncodeRLE(b[:])
}

// DecodeChunkRLE 从 RLE 数据恢复到 Chunk。
func DecodeChunkRLE(c *Chunk, runs []RLERun) bool {
	blocks := DecodeRLE(runs)
	return c.LoadBlocks(blocks)
}

// MarshalRLE 将 RLE 编码为紧凑二进制(每个 run: 2 字节 Block + 2 字节 Count)。
func MarshalRLE(runs []RLERun) []byte {
	buf := make([]byte, 4*len(runs))
	for i, r := range runs {
		binary.LittleEndian.PutUint16(buf[i*4:], r.Block)
		binary.LittleEndian.PutUint16(buf[i*4+2:], r.Count)
	}
	return buf
}

// UnmarshalRLE 从二进制还原 RLE。
func UnmarshalRLE(data []byte) []RLERun {
	if len(data)%4 != 0 {
		return nil
	}
	runs := make([]RLERun, len(data)/4)
	for i := range runs {
		runs[i].Block = binary.LittleEndian.Uint16(data[i*4:])
		runs[i].Count = binary.LittleEndian.Uint16(data[i*4+2:])
	}
	return runs
}

// ChunkDelta 区块增量:只含变更方块的位置和新值(用于增量同步)。
type ChunkDelta struct {
	Pos     ChunkPos
	Changes []BlockEntry
}

// BlockEntry 增量中的单个方块。
type BlockEntry struct {
	// 局部偏移(扁平索引,0 ~ blocksPerChunk-1)。
	// 用扁平索引而非 x,y,z 减少传输开销。
	Offset uint16
	Block  BlockID
}

// DeltaFromChanges 将 CommittedMutation 中属于 chunkPos 的变更转换为 ChunkDelta。
func DeltaFromChanges(chunkPos ChunkPos, changes []BlockChange) *ChunkDelta {
	var entries []BlockEntry
	for _, ch := range changes {
		if ch.Pos.ToChunkPos() != chunkPos {
			continue
		}
		x, y, z := ch.Pos.ToLocal()
		entries = append(entries, BlockEntry{
			Offset: uint16(index(x, y, z)),
			Block:  ch.NewBlock,
		})
	}
	if len(entries) == 0 {
		return nil
	}
	return &ChunkDelta{Pos: chunkPos, Changes: entries}
}

// ApplyDelta 将增量应用到 Chunk。
func ApplyDelta(c *Chunk, delta *ChunkDelta) {
	if delta == nil {
		return
	}
	for _, e := range delta.Changes {
		x, y, z := deindex(int(e.Offset))
		c.Set(x, y, z, e.Block)
	}
}
