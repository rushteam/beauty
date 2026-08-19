# grpc-service-discovery —— gRPC 服务发现（服务端）

演示 gRPC 服务端自动读取已注册的 protobuf 服务，并分别注册到 etcd / Nacos。

## 运行

需本地 etcd（`:2379`）和/或 Nacos（`:8848`）：

```bash
go run ./examples/grpc-service-discovery
```

服务监听 `:58080`，`Greeter` 与 `UserService` 作为独立服务名写入注册中心。

## 说明

- `grpcserver.WithAutoServiceDiscovery([]discover.Registry{...})` 自动扫描 `RegisterService` 的服务描述符。
- `WithRegionInfo` / `WithEnvironment` / `WithWeight` 写入元数据，供客户端地域过滤与负载均衡。
- 示例用手写 `ServiceDesc` 模拟 protobuf 生成代码；实际项目用 `RegisterXxxServer(s, impl)` 即可。
