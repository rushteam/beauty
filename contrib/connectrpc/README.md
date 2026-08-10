# contrib/connectrpc —— Connect 协议一等公民集成(独立模块)

基于 [connectrpc.com/connect](https://connectrpc.com) 的 Protobuf RPC 服务集成，与 `grpcserver`
对等的一等公民服务类型。Connect 底层就是标准 `net/http`，同时兼容 **Connect、gRPC、gRPC-Web**
三种线上协议。

```bash
go get github.com/rushteam/beauty/contrib/connectrpc@latest
```

## 为什么用 Connect 而不是 gRPC

| | gRPC (`grpcserver`) | Connect (`connectrpc`) |
|---|---|---|
| 底层传输 | 自建 HTTP/2 栈 | 标准 `net/http` |
| Handler 类型 | `grpc.Server` | `http.Handler` |
| 客户端 | `grpc.ClientConn` | `http.Client` |
| HTTP 中间件 | 不兼容 | 直接可用 |
| curl 可调试 | 不行 | 可以 |
| gRPC 协议兼容 | 原生 | 兼容（同一 handler 同时支持） |

## 服务端用法

```go
import (
    connectserver "github.com/rushteam/beauty/contrib/connectrpc"
    "connectrpc.com/connect"
    "connectrpc.com/validate"
    pingv1connect "example.com/gen/ping/v1/pingv1connect"
)

srv := connectserver.New(":8080",
    connectserver.WithServiceName("my-service"),
    connectserver.WithVersion("v1.0.0"),
    connectserver.WithMiddleware(accesslog.Handler, cors.Handler), // HTTP 中间件直接用
)

// 注册 protoc-gen-connect-go 生成的 handler
srv.Handle(pingv1connect.NewPingServiceHandler(&PingServer{},
    connect.WithInterceptors(validate.NewInterceptor()),
))

// 也可以混合注册普通 HTTP 路由
srv.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
    fmt.Fprint(w, "ok")
})

app := beauty.New(beauty.WithService(srv))
app.Start(ctx)
```

### 搭配服务发现

```go
etcdRegistry := etcdv3.NewRegistry(&etcdv3.Config{
    Endpoints: []string{"127.0.0.1:2379"},
    Prefix:    "/beauty",
    TTL:       10,
})

srv := connectserver.New(":8080",
    connectserver.WithAutoServiceDiscovery(
        []discover.Registry{etcdRegistry},
    ),
    connectserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
    connectserver.WithEnvironment("production"),
)

// 每个 Handle 注册的 protobuf 服务会自动按全限定名注册到注册中心
srv.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}))
srv.Handle(userv1connect.NewUserServiceHandler(&UserServer{}))

app := beauty.New(beauty.WithService(srv))
```

## 客户端用法

`Transport` 实现 `http.RoundTripper`，集成服务发现 + 轮询负载均衡：

```go
import connectserver "github.com/rushteam/beauty/contrib/connectrpc"

rt := connectserver.NewTransport(discovery, "acme.ping.v1.PingService")
defer rt.Close()

httpClient := &http.Client{Transport: rt}
client := pingv1connect.NewPingServiceClient(httpClient, "http://acme.ping.v1.PingService/")

res, err := client.Ping(ctx, &pingv1.PingRequest{Number: 42})
```

Transport 优先通过 `Watch` 实时感知实例变化；Watch 不可用时按 `WithRefreshInterval` 定时轮询。

## 默认行为

- **H2C**：默认启用（HTTP/2 Cleartext），gRPC 客户端可直连；TLS 模式自动禁用 h2c。
- **健康检查**：自动注册 `grpc.health.v1.Health`，兼容 grpc-health-probe 和 K8s gRPC 探针。
- **OTel**：自动包裹 `otelhttp`，链路追踪开箱即用。
- **优雅关闭**：默认 30s 超时，可通过 `WithShutdownTimeout` 调整。

## 选项一览

| 选项 | 说明 | 默认值 |
|---|---|---|
| `WithServiceName` | 服务名（日志/OTel span） | `"connect-server"` |
| `WithH2C` | 启用 HTTP/2 Cleartext | `true` |
| `WithHealthCheck` | 自动注册 gRPC 健康检查 | `true` |
| `WithMiddleware` | HTTP 中间件链 | 无 |
| `WithShutdownTimeout` | 优雅关闭超时 | `30s` |
| `WithReadTimeout` | HTTP 读超时 | `0`（不限） |
| `WithWriteTimeout` | HTTP 写超时 | `0`（不限） |
| `WithIdleTimeout` | 空闲连接超时 | `0`（不限） |
| `WithTLS` / `WithTLSConfig` | 启用 HTTPS | 无 |
| `WithAutoServiceDiscovery` | 按 protobuf 服务名注册到注册中心 | 关闭 |
| `WithVersion` | 版本（metadata） | 无 |
| `WithWeight` | 权重（负载均衡） | 无 |
| `WithRegionInfo` | 地域信息（Polaris 兼容） | 无 |
| `WithEnvironment` | 环境标识 | 无 |
