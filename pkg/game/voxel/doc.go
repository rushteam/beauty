// Package voxel 提供体素世界的服务端原语:区块存储、变更追踪、增量编码与快照。
//
// 设计原则与同级包一致 —— 提供机制,不绑策略:
//   - 不内置序列化格式(Payload 由业务定义)
//   - 不绑定网络协议(Delta/Snapshot 可序列化为 JSON、protobuf 或自定义)
//   - 不包含客户端渲染逻辑(meshing/LOD 由客户端自行处理)
//
// 核心组件:
//
//   - Chunk        — 固定尺寸 3D 方块数组(默认 16×16×16),O(1) 读写
//   - World        — Chunk 管理器:加载/卸载、跨 Chunk 坐标转换、邻居查询
//   - Mutation     — 变更日志:逐块修改记录 + 递增 Revision(用于增量同步)
//   - RLE          — Run-Length 压缩:Chunk 数据的紧凑网络传输
//   - Snapshot     — 世界快照:多 Chunk 的一致性切面(用于持久化/崩溃恢复)
//
// 子包:
//
//	octree — 八叉树 3D 空间索引(四叉树的 3D 版),用于体素世界中的实体 AOI
//
// 典型组合:
//
//	World + Mutation + gameloop    → 权威 tick 内批量修改、产出增量
//	RLE + ws/quic                 → 高效传输区块数据
//	Snapshot + kvstore/cache      → 持久化世界状态
//	octree + replicate            → 体素世界中的实体状态同步
package voxel
