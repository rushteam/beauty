# contrib/codec/kitex —— Kitex 注册中心格式编解码(独立模块)

提供 Kitex 兼容的服务发现编解码实现，使 beauty 服务能以 Kitex 原生格式注册到 etcd、nacos 等
注册中心，让 Kitex 客户端直接发现调用。

```bash
go get github.com/rushteam/beauty/contrib/codec/kitex@latest
```

## 注册格式

### etcd (KVCodec)

Kitex 在 etcd 中的注册格式（来自 [kitex-contrib/registry-etcd](https://github.com/kitex-contrib/registry-etcd)）：

- **key**: `{prefix}/{serviceName}{addr}`（默认 prefix `kitex/registry-etcd`）
- **value**: JSON `instanceInfo`

```json
{
    "network": "tcp",
    "address": "10.0.1.5:8888",
    "weight": 100,
    "tags": {"env": "prod"}
}
```

### Nacos/Consul (Codec)

Kitex 的 nacos/consul 注册格式与标准格式兼容，`NewCodec()` 提供过滤策略（Accept 所有 kind，
Kitex 不按 kind 区分）。

## 用法

### etcd 注册成 Kitex 格式

```go
import kitexcodec "github.com/rushteam/beauty/contrib/codec/kitex"

reg := etcdv3.NewRegistry(&etcdv3.Config{
    Endpoints: []string{"127.0.0.1:2379"},
    Codec:     kitexcodec.NewKVCodec("kitex/registry-etcd"),
})
```

### Nacos 使用 Kitex 兼容过滤

```go
import kitexcodec "github.com/rushteam/beauty/contrib/codec/kitex"

nacosReg := nacos.NewRegistry(&nacos.Config{
    Addr:  []string{"127.0.0.1:8848"},
    Codec: kitexcodec.NewCodec(),
})
```

## 完整示例

```go
import (
    beautyKitex "github.com/rushteam/beauty/contrib/kitex"
    kitexcodec "github.com/rushteam/beauty/contrib/codec/kitex"
)

// beauty 服务注册成 Kitex 格式，Kitex 客户端可直接发现
srv := beautyKitex.New(":8888",
    beautyKitex.WithServiceName("example.shop.item"),
)
itemservice.RegisterService(srv.Server(), new(ItemServiceImpl))

app := beauty.New(
    beauty.WithService(srv),
    beauty.WithRegistry(etcdv3.NewRegistry(&etcdv3.Config{
        Endpoints: []string{"127.0.0.1:2379"},
        Codec:     kitexcodec.NewKVCodec("kitex/registry-etcd"),
    })),
)
```

## 注意事项

- 本模块**不依赖** `cloudwego/kitex`（纯编解码），仅依赖 beauty core。
- 与 `contrib/codec/gozero`、`contrib/codec/kratos` 是同级模块，可按需组合。
- metadata 中的 `weight` 字段会自动映射为 Kitex instanceInfo 的 weight。
