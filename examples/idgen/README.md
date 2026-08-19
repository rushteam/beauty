# idgen —— 分布式 ID 生成

演示 `pkg/idgen` 的 Snowflake 算法：64 位趋势递增、并发唯一、可解析时间/节点/序列。

## 运行

```bash
go run ./examples/idgen
```

## 说明

- `idgen.New(nodeID)` 创建生成器；同一部署内每个实例分配唯一 nodeID（0–1023）。
- `MustNext()` 生成 ID；同一节点连续调用趋势递增，不同节点天然不冲突。
- `idgen.Parse(id)` 解析出时间戳、节点 ID、序列号；适合排查与分库分表路由。
- 典型场景：对局 ID、订单号、消息序号、数据库主键。
