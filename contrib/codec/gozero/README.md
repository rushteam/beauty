# contrib/codec/gozero —— go-zero 注册中心格式编解码(独立模块)

提供 go-zero 兼容的服务发现编解码,使 beauty 服务能以 go-zero 原生格式注册到 etcd、nacos 等
注册中心,让 go-zero 客户端直接发现调用。

```bash
go get github.com/rushteam/beauty/contrib/codec/gozero@latest
```

## 注册格式

go-zero 在 etcd 中的格式:

- **key**: `{serviceName}/{leaseId}`
- **value**: `host:port`(纯文本,无 JSON)

## 用法

### etcd (KVCodec)

```go
import gozerocodec "github.com/rushteam/beauty/contrib/codec/gozero"

reg := etcdv3.NewRegistry(&etcdv3.Config{
    Endpoints: []string{"127.0.0.1:2379"},
    Codec:     gozerocodec.NewKVCodec(),
})
```

### Nacos/Consul (Codec)

```go
nacosReg := nacos.NewRegistry(&nacos.Config{
    Addr:  []string{"127.0.0.1:8848"},
    Codec: gozerocodec.NewCodec(), // Accept 所有服务(go-zero 不按 kind 区分)
})
```

`init()` 自动注册 `"gozero"` 到 `discover.RegisterCodec` / `RegisterKVCodec`,也可通过
`discover.Codec("gozero")` 按名获取。

## 边界

本模块**不依赖** go-zero(纯编解码),仅依赖 beauty core。与 `contrib/codec/kitex`、
`contrib/codec/kratos` 同级,可按需组合,不可混用同一 etcd 前缀下的不同格式。
