# kvstore-shared —— KV 存储

演示 `pkg/kvstore` 让 counter、cooldown、idempotency 跨实例共享状态，从单进程内存升级为多实例一致。

## 运行

```bash
go run ./examples/kvstore-shared
```

## 说明

- 两个「实例」共用 `kvstore.NewMemory()`；生产替换为 Redis 实现的 `kvstore.Store`。
- **counter**：负载均衡打到不同实例，配额仍统一计数（示例 3 次限额第 4 次被拒）。
- **cooldown**：实例 1 触发冷却，实例 2 无法重复领取。
- **idempotency**：实例 1 处理请求，重试打到实例 2 复用结果、不重复执行。
- 各原语通过 `WithStore(store)` 注入，业务代码无需改动。
