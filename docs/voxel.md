# 体素世界原语（pkg/game/voxel）

beauty 在 `pkg/game/` 下新增了体素世界的服务端原语包,覆盖 Minecraft 类体素游戏的核心
服务端需求:区块存储、方块变更追踪、增量同步、RLE 压缩传输、世界快照与恢复、3D 空间索引。

遵循 beauty 风格:机制不绑策略、纯标准库、泛型、并发安全。

## 组件全景

| 组件 | 一句话 | 典型场景 |
|------|--------|----------|
| `Chunk` | 16³ 方块数组,O(1) 读写 | 区块加载/卸载、方块存取 |
| `World` | Chunk 管理器 | 跨区块坐标转换、范围查询 |
| `Mutation` + `MutationLog` | 变更追踪 + 环形日志 | 增量同步、断线重连追赶 |
| `EncodeRLE` / `DecodeRLE` | Run-Length 压缩 | 区块网络传输(压缩率可达 99%) |
| `ChunkDelta` / `ApplyDelta` | 方块级增量 | 逐帧增量同步(只发变化的方块) |
| `WorldSnapshot` / `SnapshotRing` | 世界快照 + 环形缓冲 | 持久化、崩溃恢复 |
| `octree.Tree` | 八叉树 3D 空间索引 | 体素世界中的实体 AOI |

## 架构图

```
        客户端 ←── ws/quic ──→ 服务端
          │                      │
          │  ┌───────────────────┤
          │  │                   │
          ▼  ▼                   ▼
     RLE 解码              World + Mutation
     ApplyDelta               │
     octree(客户端实体)    ┌───┴───┐
                          │       │
                       Chunk   Mutation
                       存储     变更追踪
                          │       │
                          ▼       ▼
                    ┌─────────────────┐
                    │  增量同步出口    │
                    │  RLE(全量)      │
                    │  Delta(增量)    │
                    │  CatchUp(追赶)  │
                    └─────────────────┘
                          │
                    Snapshot(持久化)
```

## 速查:核心类型

### 坐标系

```go
// 方块在世界中的绝对坐标(整数)
pos := voxel.BlockPos{X: 5, Y: 64, Z: -17}

// 转换为区块坐标
cp := pos.ToChunkPos()     // ChunkPos{0, 4, -2}

// 转换为区块内局部坐标(0~15)
lx, ly, lz := pos.ToLocal() // (5, 0, 15)

// 负坐标正确处理(-17 → chunk -2, local 15)
voxel.BlockPos{-17, 0, 0}.ToChunkPos() // ChunkPos{-2, 0, 0}

// 邻居查询
cp.Neighbors6()  // 6 个正交邻居(±X, ±Y, ±Z)
cp.Neighbors26() // 26 个邻居(含对角)
```

### Chunk（区块存储）

```go
c := voxel.NewChunk(voxel.ChunkPos{0, 0, 0})

c.Get(5, 10, 3)          // 读取局部坐标,越界返回 Air
c.Set(5, 10, 3, 42)      // 写入,返回旧值,递增 Revision
c.Fill(Stone)             // 填充整个区块
c.IsEmpty()               // 是否全为 Air
c.NonAirCount()           // 非空气方块数
c.Revision()              // 修订号(每次 Set 递增)
c.Blocks()                // 底层数组拷贝(序列化用)
c.LoadBlocks(data)        // 从外部数据恢复(反序列化)
c.ForEach(func(x, y, z int, block voxel.BlockID) {
    // 遍历所有非 Air 方块
})
```

- 内部用扁平数组 `[4096]uint16`(16³),缓存友好
- 并发安全(读写锁)
- 相同值写入不递增 Revision

### World（世界管理）

```go
w := voxel.NewWorld()

// 区块管理
c, created := w.LoadChunk(pos)   // 加载/创建区块
w.UnloadChunk(pos)               // 卸载(返回 Chunk 用于持久化)
w.Chunk(pos)                     // 查询(不存在返回 nil)
w.HasChunk(pos)                  // 是否已加载
w.ChunkCount()                   // 已加载区块数

// 跨区块方块读写(自动处理坐标转换)
w.GetBlock(voxel.BlockPos{-1, 64, 33})    // 未加载返回 Air
w.SetBlock(voxel.BlockPos{-1, 64, 33}, 1) // 未加载自动创建区块

// 范围查询
w.ChunksInRadius(center, 3)      // 半径 3 内已加载区块(切比雪夫距离)
w.ForEachChunk(func(pos, c) {})  // 遍历所有已加载区块
w.LoadedChunks()                 // 所有已加载区块坐标快照
```

## 速查:变更追踪 + 增量同步

### Mutation（变更追踪）

```go
m := voxel.NewMutation()

// 方式一:手动记录
w.SetBlock(pos, block)
m.Record(voxel.BlockChange{Pos: pos, OldBlock: old, NewBlock: block})

// 方式二:一步完成(推荐)
m.RecordSet(w, pos, block) // SetBlock + 自动 Record

// 每 tick 结束时提交
cm := m.Commit() // 取走本帧变更,递增 Revision;无变更返回 nil
```

### MutationLog（断线重连追赶）

```go
log := voxel.NewMutationLog(128) // 保留最近 128 帧

// 每 tick 追加
if cm := m.Commit(); cm != nil {
    log.Append(*cm)
}

// 客户端断线重连:追赶 revision > lastKnown 的所有变更
changes, ok := log.Since(lastKnown)
if !ok {
    // 太旧,需要全量同步
}
```

### ChunkDelta（方块级增量）

```go
// 从 CommittedMutation 提取某个 Chunk 的增量
delta := voxel.DeltaFromChanges(chunkPos, cm.Changes)

// 客户端应用增量
voxel.ApplyDelta(clientChunk, delta)
```

## 速查:RLE 压缩传输

体素世界大量连续相同方块(空气层、石头层),RLE 压缩效果极好。

```go
// 压缩
runs := voxel.EncodeChunkRLE(chunk)
data := voxel.MarshalRLE(runs) // 紧凑二进制(4字节/游程)

// 解码
runs = voxel.UnmarshalRLE(data)
voxel.DecodeChunkRLE(targetChunk, runs)
```

**压缩效果参考**(16³ 区块,原始 8192 字节):

| 地形类型 | 游程数 | 压缩后字节 | 压缩率 |
|----------|--------|-----------|--------|
| 纯空气 | 1 | 4 | 0.05% |
| 半石头半空气 | 2~32 | 8~128 | 0.1%~1.6% |
| 典型地表(石/土/草/空) | 8~64 | 32~256 | 0.4%~3% |
| 完全随机 | ~4096 | ~16384 | 200%(不适用) |

## 速查:世界快照

```go
// 拍摄一致性快照
snap := voxel.TakeSnapshot(w, mutation)

// 恢复(清除现有区块,加载快照中的区块)
voxel.RestoreSnapshot(w2, snap)

// 快照环形缓冲(保留最近 N 个)
ring := voxel.NewSnapshotRing(8)
ring.Push(snap)
latest, ok := ring.Latest()
snap, rev, ok := ring.Nearest(revision)  // <= revision 的最近快照
```

## 速查:octree（八叉树 3D 空间索引）

`quadtree` 的 3D 版,用于体素世界中的实体(玩家、怪物、掉落物)AOI。

```go
tree := octree.New[string](
    octree.Box{X: 0, Y: 0, Z: 0, W: 1024, H: 256, D: 1024}, // 世界边界
    16,  // 节点容量(推荐 8~16)
    10,  // 最大深度
)

tree.Add("player1", 50, 64, 50)
tree.Move("player1", 55, 64, 48)
tree.Remove("zombie1")

// 球形范围查询(按距离排序)
nearby := tree.Nearby(50, 64, 50, 32, "player1") // 排除自己

// K 近邻
knn := tree.KNN(50, 64, 50, 5, 100) // 最近 5 个,搜索半径 100

// 包围盒查询
entities := tree.QueryBox(octree.Box{X: 40, Y: 60, Z: 40, W: 20, H: 10, D: 20})

tree.Pos("player1") // 查坐标
tree.Len()           // 实体总数
```

- 递归细分拥挤区域、合并空旷区域,自动适应密度
- 并发安全(单读写锁)
- 与 `spatial`(2D) / `quadtree`(2D) 互补,面向有高度维度的场景

## 与既有 game 包的组合

### gameloop + voxel（权威 tick 内修改世界）

```go
type VoxelHandler struct {
    world    *voxel.World
    mutation *voxel.Mutation
    log      *voxel.MutationLog
}

func (h *VoxelHandler) OnTick(frame uint64, inputs []PlayerInput) []Output {
    for _, in := range inputs {
        switch in.Action {
        case "dig":
            h.mutation.RecordSet(h.world, in.Pos, voxel.Air)
        case "place":
            h.mutation.RecordSet(h.world, in.Pos, in.Block)
        }
    }
    cm := h.mutation.Commit()
    if cm != nil {
        h.log.Append(*cm)
        return []Output{{Type: "delta", Data: cm}}
    }
    return nil
}
```

### ws/quic + RLE（区块传输）

```go
// 玩家进入视野 → 发送完整区块(RLE 压缩)
runs := voxel.EncodeChunkRLE(chunk)
data := voxel.MarshalRLE(runs)
conn.Send(data) // 8KB 原始 → 通常 < 100 字节

// 每 tick → 发送增量
delta := voxel.DeltaFromChanges(chunkPos, cm.Changes)
conn.SendJSON(delta) // 只含变化的方块
```

### octree + replicate（实体状态同步）

```go
// 八叉树管理实体位置
tree.Move(entityID, x, y, z)

// 结合 replicate 做 AOI
nearby3D := tree.Nearby(viewer.X, viewer.Y, viewer.Z, viewRadius)
// 转换为 spatial.Entity 格式后喂给 Projector
delta := projector.Project(frame, viewer, visible, dirty, removed, lookup)
```

### snapbuf + snapshot（世界快照 rewind）

```go
// 每 N tick 保存快照(用于回滚/调试)
if frame%100 == 0 {
    snap := voxel.TakeSnapshot(world, mutation)
    snapshotRing.Push(snap)
}

// 回滚到某个版本
if snap, ok := snapshotRing.At(targetRevision); ok {
    voxel.RestoreSnapshot(world, snap)
}
```

## 互补关系速记

| 需求 | 用这个 | 而不是 | 因为 |
|------|--------|--------|------|
| 平面地图 2D 实体 | `spatial` / `quadtree` | `octree` | 前者无 Z 轴开销 |
| 体素世界 3D 实体 | `octree` | `spatial` | 后者无高度维度 |
| 2D 地图方块存储 | 业务自定义 2D 数组 | `voxel.Chunk` | Chunk 是 3D 的 |
| 3D 方块存储 | `voxel.Chunk` / `World` | 手写 map | Chunk 扁平数组更高效 |
| 实体状态增量同步 | `replicate` | `voxel.Mutation` | replicate 管实体;Mutation 管方块 |
| 方块变更增量同步 | `voxel.Mutation` + `ChunkDelta` | `replicate` | 方块变更是体素特有问题 |
| 世界快照 rewind | `voxel.SnapshotRing` | `snapbuf.Ring` | 前者存完整世界;后者存单帧状态 |

## 性能参考

| 操作 | 耗时 | 说明 |
|------|------|------|
| `Chunk.Get/Set` | ~10ns | 扁平数组索引 + 读写锁 |
| `RLE 编码` (半填充 chunk) | ~15µs | Benchmark 实测 |
| `RLE 压缩后大小` (典型地形) | 32~256 字节 | 原始 8192 字节 |
| `octree.Nearby` (1 万实体, r=50) | ~20µs | 类似 quadtree benchmark |
| `World.SetBlock` (跨区块) | ~50ns | 含坐标转换 + Chunk 查找 |

## demo

`examples/voxel/main.go`,单文件可直接 `go run`,演示完整工作流:
地形生成 → 方块操作 → RLE 压缩 → 增量同步 → 断线追赶 → 快照恢复 → 八叉树实体管理。
