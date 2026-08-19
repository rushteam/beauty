# grpc-label-filter —— gRPC 标签过滤

演示 `grpcclient.NewLabelFilter` 的各种过滤写法：精确匹配、in/notin、存在性检查。

## 运行

需 etcd 在 `localhost:2379`，且已有注册实例（可先跑 `grpc-service-discovery`）：

```bash
go run ./examples/grpc-label-filter
```

## 说明

- **基础**：`WithMatchLabel("region", "us-west-1")` 精确匹配；`WithRegionIn` / `WithEnvironmentIn` 多选。
- **高级**：`WithExpression("tier", FilterOpIn, ...)` 集合过滤；`FilterOpExists` / `FilterOpNotExist` 标签存在性。
- **复杂**：多条件 AND 组合 + `NewServiceDiscoveryClient` 配合健康检查与 failover。
- 与 `WithDiscoveryRegionFilter` 向后兼容，可渐进迁移到新 API。
