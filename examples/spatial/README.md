# spatial —— 空间 AOI

演示 `pkg/spatial` 的网格空间索引：`Nearby` 范围查询与 `KNN` 最近 N 个实体。

## 运行

```bash
go run ./examples/spatial
```

## 说明

- `spatial.New[string](cellSize)` 创建索引；`cellSize` 建议设为典型查询半径量级（示例 100 米）。
- `Add` / `Move` / `Remove` 维护实体坐标；`Nearby(x, y, radius, exclude)` 返回范围内实体（按距离排序）。
- `KNN` 查找最近 N 个，可设最大搜索半径避免全图扫描。
- 典型场景：LBS「附近的人」、MMO 兴趣区域（AOI）、大地图分区。
