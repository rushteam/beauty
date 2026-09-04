// voxel 示例:体素世界的核心工作流。
//
// 演示:创建世界 → 修改方块 → 变更追踪 → RLE 压缩 → 快照 → 恢复 → 八叉树实体管理。
// 场景:Minecraft 类体素游戏服务端、可破坏地形、沙盒建造。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/game/voxel"
	"github.com/rushteam/beauty/pkg/game/voxel/octree"
)

// 方块类型常量(业务自定义)。
const (
	Stone BlockID = iota + 1
	Dirt
	Grass
	Water
	Sand
)

type BlockID = voxel.BlockID

func main() {
	// ═══════════════════════════════════════
	// 1. 创建世界 + 生成简单地形
	// ═══════════════════════════════════════
	w := voxel.NewWorld()
	m := voxel.NewMutation()

	fmt.Println("=== 1. 生成地形 ===")
	// 在 chunk(0,0,0) 里生成一个简单地形:底部石头,中间泥土,顶部草地
	for x := 0; x < voxel.ChunkSize; x++ {
		for z := 0; z < voxel.ChunkSize; z++ {
			for y := 0; y < 4; y++ {
				m.RecordSet(w, voxel.BlockPos{X: int32(x), Y: int32(y), Z: int32(z)}, Stone)
			}
			for y := 4; y < 7; y++ {
				m.RecordSet(w, voxel.BlockPos{X: int32(x), Y: int32(y), Z: int32(z)}, Dirt)
			}
			m.RecordSet(w, voxel.BlockPos{X: int32(x), Y: 7, Z: int32(z)}, Grass)
		}
	}
	cm := m.Commit()
	fmt.Printf("地形生成完毕: %d 个方块变更, revision=%d\n", len(cm.Changes), cm.Revision)

	c := w.Chunk(voxel.ChunkPos{X: 0, Y: 0, Z: 0})
	fmt.Printf("chunk(0,0,0): %d 个非空气方块\n", c.NonAirCount())

	// ═══════════════════════════════════════
	// 2. 玩家挖方块 + 放方块
	// ═══════════════════════════════════════
	fmt.Println("\n=== 2. 玩家操作 ===")
	// 玩家在 (5,7,5) 挖掉草地
	m.RecordSet(w, voxel.BlockPos{X: 5, Y: 7, Z: 5}, voxel.Air)
	// 玩家在 (5,8,5) 放一块石头
	m.RecordSet(w, voxel.BlockPos{X: 5, Y: 8, Z: 5}, Stone)

	cm2 := m.Commit()
	fmt.Printf("玩家操作: %d 个变更, revision=%d\n", len(cm2.Changes), cm2.Revision)
	for _, ch := range cm2.Changes {
		fmt.Printf("  (%d,%d,%d): %d → %d\n", ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.OldBlock, ch.NewBlock)
	}

	// ═══════════════════════════════════════
	// 3. RLE 压缩(网络传输)
	// ═══════════════════════════════════════
	fmt.Println("\n=== 3. RLE 压缩 ===")
	runs := voxel.EncodeChunkRLE(c)
	raw := voxel.MarshalRLE(runs)
	fmt.Printf("原始大小: %d 字节 (16×16×16×2)\n", voxel.ChunkSize*voxel.ChunkSize*voxel.ChunkSize*2)
	fmt.Printf("RLE 压缩: %d 个游程, %d 字节 (压缩率 %.1f%%)\n",
		len(runs), len(raw), float64(len(raw))/float64(voxel.ChunkSize*voxel.ChunkSize*voxel.ChunkSize*2)*100)

	// 客户端收到后解码
	decoded := voxel.UnmarshalRLE(raw)
	c2 := voxel.NewChunk(voxel.ChunkPos{X: 0, Y: 0, Z: 0})
	voxel.DecodeChunkRLE(c2, decoded)
	fmt.Printf("解码验证: (5,8,5)=%d (应为 Stone=%d)\n", c2.Get(5, 8, 5), Stone)

	// ═══════════════════════════════════════
	// 4. 增量同步(只发变化的方块)
	// ═══════════════════════════════════════
	fmt.Println("\n=== 4. 增量同步 ===")
	delta := voxel.DeltaFromChanges(voxel.ChunkPos{X: 0, Y: 0, Z: 0}, cm2.Changes)
	fmt.Printf("增量: chunk(%d,%d,%d), %d 个方块变更\n",
		delta.Pos.X, delta.Pos.Y, delta.Pos.Z, len(delta.Changes))

	// 客户端应用增量
	c3 := voxel.NewChunk(voxel.ChunkPos{X: 0, Y: 0, Z: 0})
	c3.Fill(Grass) // 模拟客户端已有的旧数据
	voxel.ApplyDelta(c3, delta)
	fmt.Printf("应用增量后: (5,7,5)=%d (应为 Air=%d), (5,8,5)=%d (应为 Stone=%d)\n",
		c3.Get(5, 7, 5), voxel.Air, c3.Get(5, 8, 5), Stone)

	// ═══════════════════════════════════════
	// 5. 变更日志(断线重连追赶)
	// ═══════════════════════════════════════
	fmt.Println("\n=== 5. 断线重连追赶 ===")
	log := voxel.NewMutationLog(128)
	log.Append(*cm)
	log.Append(*cm2)

	// 玩家断线前的 revision=1,重连后追赶
	catchup, ok := log.Since(1)
	fmt.Printf("追赶 rev>1: ok=%v, %d 帧变更\n", ok, len(catchup))
	for _, cu := range catchup {
		fmt.Printf("  revision=%d, %d 个方块\n", cu.Revision, len(cu.Changes))
	}

	// ═══════════════════════════════════════
	// 6. 世界快照(持久化 / 崩溃恢复)
	// ═══════════════════════════════════════
	fmt.Println("\n=== 6. 世界快照 ===")
	snap := voxel.TakeSnapshot(w, m)
	fmt.Printf("快照: revision=%d, %d 个区块\n", snap.Revision, len(snap.Chunks))

	// 模拟崩溃恢复
	w2 := voxel.NewWorld()
	voxel.RestoreSnapshot(w2, snap)
	fmt.Printf("恢复后: %d 个区块, (5,8,5)=%d\n",
		w2.ChunkCount(), w2.GetBlock(voxel.BlockPos{X: 5, Y: 8, Z: 5}))

	// 快照环形缓冲(保留最近 N 个快照)
	ring := voxel.NewSnapshotRing(4)
	ring.Push(snap)
	latest, _ := ring.Latest()
	fmt.Printf("快照缓冲: 最新 revision=%d\n", latest.Revision)

	// ═══════════════════════════════════════
	// 6b. 增量快照(大型世界优化)
	// ═══════════════════════════════════════
	fmt.Println("\n=== 6b. 增量快照(性能优化) ===")
	// 清除脏标记(模拟上次全量快照后的状态)
	w.ForEachChunk(func(_ voxel.ChunkPos, c *voxel.Chunk) {
		// IsDirty + markClean 内部管理
	})
	// 只修改 1 个方块
	m.RecordSet(w, voxel.BlockPos{X: 3, Y: 3, Z: 3}, Water)
	m.Commit()
	incrSnap := voxel.TakeIncrementalSnapshot(w, m)
	fmt.Printf("增量快照: %d 个脏区块(vs 全量 %d 个区块)\n", len(incrSnap.Chunks), w.ChunkCount())
	// 与基线合并得到完整快照
	merged := voxel.MergeSnapshot(snap, incrSnap)
	fmt.Printf("合并后: %d 个区块, revision=%d\n", len(merged.Chunks), merged.Revision)

	// ═══════════════════════════════════════
	// 7. 八叉树:体素世界中的实体管理
	// ═══════════════════════════════════════
	fmt.Println("\n=== 7. 八叉树实体管理 ===")
	// 世界范围:256×256×256 方块
	tree := octree.New[string](octree.Box{X: 0, Y: 0, Z: 0, W: 256, H: 256, D: 256}, 8, 10)

	tree.Add("玩家A", 50, 64, 50)
	tree.Add("玩家B", 55, 64, 48)
	tree.Add("僵尸", 200, 30, 180)
	tree.Add("苦力怕", 52, 64, 52)

	// 查找玩家A附近 20 格内的实体
	nearby := tree.Nearby(50, 64, 50, 20, "玩家A")
	fmt.Printf("玩家A附近 20 格内:\n")
	for _, e := range nearby {
		fmt.Printf("  %s 距离 %.1f 格\n", e.ID, e.Dist)
	}

	// 查找立方体区域内的实体
	entities := tree.QueryBox(octree.Box{X: 45, Y: 60, Z: 45, W: 15, H: 10, D: 15})
	fmt.Printf("区域 (45~60, 60~70, 45~60) 内: %d 个实体\n", len(entities))

	// 坐标转换演示
	fmt.Println("\n=== 8. 坐标转换 ===")
	pos := voxel.BlockPos{X: -17, Y: 64, Z: 33}
	cp := pos.ToChunkPos()
	lx, ly, lz := pos.ToLocal()
	fmt.Printf("世界坐标 (%d,%d,%d) → chunk(%d,%d,%d) 局部(%d,%d,%d)\n",
		pos.X, pos.Y, pos.Z, cp.X, cp.Y, cp.Z, lx, ly, lz)

	// 邻居区块
	neighbors := cp.Neighbors6()
	fmt.Printf("chunk(%d,%d,%d) 的 6 个邻居:\n", cp.X, cp.Y, cp.Z)
	for _, n := range neighbors {
		fmt.Printf("  (%d,%d,%d)\n", n.X, n.Y, n.Z)
	}
}
