# http-service-discovery —— HTTP 服务发现

演示 `pkg/client/http` 的 `ServiceDiscoveryHTTPClient`：服务发现 + WRR 加权轮询。

## 运行

```bash
go run ./examples/http-service-discovery
```

无需外部依赖，内置 3 个 `httptest` 后端（权重 1:2:3），打印命中分布。

## 说明

- 内存 `memDiscovery` 实现 `discover.Discovery`；生产替换为 etcd/nacos/consul。
- `DoWith(ctx, method, path, body)` 便捷调用；`NewRequest` + `Do` 可自定义 header。
- `HTTPWeightedRoundRobin` 按 metadata 中 `weight` 精确分配流量（一轮 6 次 → 1:2:3）。
