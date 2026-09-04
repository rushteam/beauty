package voxel

// BlockID 方块类型标识。0 表示空气(Air)。uint16 支持 65535 种方块类型。
type BlockID = uint16

// Air 空气方块(默认值)。
const Air BlockID = 0

// ChunkSize 区块边长(每轴方块数)。16 是业界标准(Minecraft 等)。
const ChunkSize = 16

// blocksPerChunk 一个 Chunk 中的方块总数。
const blocksPerChunk = ChunkSize * ChunkSize * ChunkSize

// ChunkPos 区块在世界网格中的坐标(整数,每单位代表一个 ChunkSize 的区块)。
type ChunkPos struct {
	X, Y, Z int32
}

// BlockPos 方块在世界中的绝对坐标(整数)。
type BlockPos struct {
	X, Y, Z int32
}

// ToChunkPos 将方块绝对坐标转换为所在区块坐标。
func (b BlockPos) ToChunkPos() ChunkPos {
	return ChunkPos{
		X: floorDiv(b.X, ChunkSize),
		Y: floorDiv(b.Y, ChunkSize),
		Z: floorDiv(b.Z, ChunkSize),
	}
}

// ToLocal 将方块绝对坐标转换为区块内的局部坐标(0 ~ ChunkSize-1)。
func (b BlockPos) ToLocal() (x, y, z int) {
	return int(floorMod(b.X, ChunkSize)),
		int(floorMod(b.Y, ChunkSize)),
		int(floorMod(b.Z, ChunkSize))
}

// Neighbors6 返回 ChunkPos 的 6 个正交邻居(±X, ±Y, ±Z)。
func (c ChunkPos) Neighbors6() [6]ChunkPos {
	return [6]ChunkPos{
		{c.X - 1, c.Y, c.Z}, {c.X + 1, c.Y, c.Z},
		{c.X, c.Y - 1, c.Z}, {c.X, c.Y + 1, c.Z},
		{c.X, c.Y, c.Z - 1}, {c.X, c.Y, c.Z + 1},
	}
}

// Neighbors26 返回 ChunkPos 的 26 个邻居(含对角)。
func (c ChunkPos) Neighbors26() []ChunkPos {
	out := make([]ChunkPos, 0, 26)
	for dx := int32(-1); dx <= 1; dx++ {
		for dy := int32(-1); dy <= 1; dy++ {
			for dz := int32(-1); dz <= 1; dz++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				out = append(out, ChunkPos{c.X + dx, c.Y + dy, c.Z + dz})
			}
		}
	}
	return out
}

// floorDiv 整数除法向负无穷取整(Go 的 / 是向零截断)。
func floorDiv(a, b int32) int32 {
	q := a / b
	if (a^b) < 0 && q*b != a {
		q--
	}
	return q
}

// floorMod 配合 floorDiv 的模运算,结果恒为 [0, b)。
func floorMod(a, b int32) int32 {
	return a - floorDiv(a, b)*b
}
