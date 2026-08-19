# grpc-client-discovery —— gRPC 客户端发现

演示 `grpcclient.ClientFactory` 通过 etcd 发现服务、按地域过滤并发起 RPC 调用。

## 运行

先启动 `examples/grpc-service-discovery`，并确保 etcd 在 `127.0.0.1:2379`：

```bash
go run ./examples/grpc-client-discovery
```

## 说明

- `factory.GetClient("v1alpha.Greeter")` 按服务名获取客户端；`WatchAllServices` 监听实例变更。
- `WithDiscoveryRegionFilter` 按 region/zone/campus/environment 多选过滤实例。
- `WithDiscoveryLabelFilter` 支持 `in`、`notin`、存在性检查等高级表达式。
- 示例用 `conn.Invoke` 模拟 protobuf 调用；生产环境替换为生成的 stub client。
