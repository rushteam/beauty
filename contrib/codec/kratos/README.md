# contrib/codec/kratos —— Kratos 注册中心格式编解码(独立模块)

提供 [Kratos](https://go-kratos.dev) 兼容的服务发现编解码,使 beauty 服务能以 Kratos 原生格式
注册到 etcd、nacos 等注册中心,让 Kratos 客户端直接发现调用。

```bash
go get github.com/rushteam/beauty/contrib/codec/kratos@latest
```

## 注册格式

Kratos 在 etcd 中的格式(默认 namespace `microservices`):

- **key**: `/microservices/{name}/{id}`
- **value**: JSON `ServiceInstance`

```json
{
    "id": "node-1",
    "name": "helloworld",
    "version": "v1.0.0",
    "metadata": {"env": "prod"},
    "endpoints": ["grpc://10.0.1.5:9000", "http://10.0.1.5:8000"]
}
```

## 用法

### etcd (KVCodec)

```go
import kratoscodec "github.com/rushteam/beauty/contrib/codec/kratos"

reg := etcdv3.NewRegistry(&etcdv3.Config{
    Endpoints: []string{"127.0.0.1:2379"},
    Codec:     kratoscodec.NewKVCodec("microservices"),
})
```

### Nacos/Consul (Codec)

```go
nacosReg := nacos.NewRegistry(&nacos.Config{
    Addr:  []string{"127.0.0.1:8848"},
    Codec: kratoscodec.NewCodec(), // 仅 Accept grpc kind 服务
})
```

序列化时按 beauty `Service.Kind()` 选择 endpoint scheme(`grpc`/`http` 等);反序列化优先取
`grpc://` endpoint。

## 边界

本模块**不依赖** Kratos 框架(纯编解码),仅依赖 beauty core。namespace 可通过
`NewKVCodec(namespace)` 自定义。与 `contrib/codec/gozero`、`contrib/codec/kitex` 同级。
