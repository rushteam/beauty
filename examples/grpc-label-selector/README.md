# grpc-label-selector —— gRPC 标签选择器

与 `grpc-label-filter` 同类 API，额外调用 `GetServiceInfo()` 打印过滤后的实例数量，便于验证选择器行为。

## 运行

需 etcd 在 `localhost:2379`，且已有注册实例：

```bash
go run ./examples/grpc-label-selector
```

## 说明

- 覆盖基础（精确匹配、地域过滤、多地域）、高级（in/notin/存在性）、复杂（混合条件 + 客户端管理器）三档场景。
- 每个客户端创建后立即 `GetServiceInfo()` 输出 `services_found`，直观看到过滤效果。
- `NewServiceDiscoveryClient` 演示 WRR 策略 + 健康检查 + failover 的组合配置。
