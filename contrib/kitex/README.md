# contrib/kitex —— Kitex Thrift 一等公民集成(独立模块)

基于 [cloudwego/kitex](https://github.com/cloudwego/kitex) 的高性能 Thrift RPC 服务集成，
与 `grpcserver`、`connectrpc` 对等的一等公民服务类型。

```bash
go get github.com/rushteam/beauty/contrib/kitex@latest
```

## 为什么用 Kitex

| | gRPC (`grpcserver`) | Connect (`connectrpc`) | Kitex (`kitex`) |
|---|---|---|---|
| 协议 | Protobuf/gRPC | Protobuf/Connect+gRPC | **Thrift/TTHeader** |
| 传输层 | 自建 HTTP/2 | 标准 `net/http` | Netpoll (高性能事件驱动) |
| 序列化 | Protobuf | Protobuf | **Thrift / Protobuf** |
| 生态 | 通用 gRPC 生态 | HTTP 中间件通用 | CloudWeGo 生态 |

Kitex 在高并发场景下性能优异，原生支持 Thrift IDL + TTHeader 传输协议，适合对延迟要求严格的微服务。

## 服务端用法

```go
import (
    beautyKitex "github.com/rushteam/beauty/contrib/kitex"
    kserver "github.com/cloudwego/kitex/server"
    "github.com/cloudwego/kitex/pkg/transmeta"
    "your_project/kitex_gen/item/itemservice"
)

srv := beautyKitex.New(":8888",
    beautyKitex.WithServiceName("example.shop.item"),
    beautyKitex.WithWeight(100),
    // 透传 Kitex 原生选项
    beautyKitex.WithKitexServerOptions(
        kserver.WithMetaHandler(transmeta.ServerTTHeaderHandler),
    ),
)

// 注册 Kitex 生成的 handler
itemservice.RegisterService(srv.Server(), new(ItemServiceImpl))

app := beauty.New(beauty.WithService(srv))
app.Start(ctx)
```

### 搭配服务发现

```go
import kitexcodec "github.com/rushteam/beauty/contrib/codec/kitex"

etcdRegistry := etcdv3.NewRegistry(&etcdv3.Config{
    Endpoints: []string{"127.0.0.1:2379"},
    Codec:     kitexcodec.NewKVCodec("kitex/registry-etcd"),
})

srv := beautyKitex.New(":8888",
    beautyKitex.WithServiceName("example.shop.item"),
    beautyKitex.WithAutoServiceDiscovery(
        []discover.Registry{etcdRegistry},
    ),
    beautyKitex.WithRegionInfo("cn-east", "shanghai", "campus1"),
    beautyKitex.WithEnvironment("production"),
)

itemservice.RegisterService(srv.Server(), new(ItemServiceImpl))

app := beauty.New(beauty.WithService(srv))
```

使用 `contrib/codec/kitex` 的 KVCodec 注册成 Kitex 原生格式，Kitex 客户端可直接发现。

## 客户端用法

`ResolverAdapter` 将 beauty 的 `discover.Discovery` 适配为 Kitex 的 `discovery.Resolver`：

```go
import (
    beautyKitex "github.com/rushteam/beauty/contrib/kitex"
    "github.com/cloudwego/kitex/client"
    "github.com/cloudwego/kitex/transport"
    "github.com/cloudwego/kitex/pkg/transmeta"
)

adapter := beautyKitex.NewResolverAdapter(etcdDiscovery)

cli := itemservice.NewClient("example.shop.item",
    client.WithResolver(adapter),
    client.WithTransportProtocol(transport.TTHeader),
    client.WithMetaHandler(transmeta.ClientTTHeaderHandler),
)

resp, err := cli.GetItem(ctx, &item.GetItemRequest{Id: 1})
```

## 选项一览

| 选项 | 说明 | 默认值 |
|---|---|---|
| `WithServiceName` | 服务名（注册中心/日志） | `"kitex-server"` |
| `WithKitexServerOptions` | 透传 Kitex 原生 `server.Option` | 无 |
| `WithAutoServiceDiscovery` | 按 Thrift 服务名注册到注册中心 | 关闭 |
| `WithMetadata` | 自定义元数据 | `{"kind": "thrift"}` |
| `WithVersion` | 版本（metadata） | 无 |
| `WithWeight` | 权重（负载均衡） | 无 |
| `WithRegionInfo` | 地域信息（Polaris 兼容） | 无 |
| `WithEnvironment` | 环境标识 | 无 |

## 搭配 codec/kitex 实现跨框架互通

beauty 服务注册成 Kitex 格式 → Kitex 客户端直接发现调用：

```
Beauty 服务 ──(codec/kitex)──→ etcd (Kitex 格式) ──→ Kitex 客户端
                                                   ──→ Beauty 客户端 (ResolverAdapter)
```

参见 [`contrib/codec/kitex`](../codec/kitex) 了解 Kitex 注册格式编解码。
