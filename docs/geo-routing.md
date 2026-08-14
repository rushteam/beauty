# 地域亲和路由 (LocalityRouter)

`LocalityRouter` 按 **campus → zone → region** 逐级 fallback,为服务间调用选择地理上最近的实例。

与 `LabelRouter`(硬匹配)不同,LocalityRouter **逐级放宽**过滤,在延迟与可用性之间折中。

## 服务端注册

```go
grpcserver.WithRegionInfo("cn-east", "cn-east-1a", "campus-1")
webserver.WithRegionInfo("cn-east", "cn-east-1a", "campus-1")
```

## 客户端路由

```go
r := router.NewLocalityRouter(router.Locality{
    Region: "cn-east",
    Zone:   "cn-east-1a",
}, router.WithGlobalFallback(true))

client := grpcclient.NewServiceDiscoveryClient(reg, "payment",
    grpcclient.WithServiceRouter(r),
)
```

## 选项

| 选项 | 作用 |
|------|------|
| `WithGlobalFallback(true)` | 所有层级无匹配时退回全量实例 |
| `WithTiers("zone", "region")` | 自定义层级顺序 |

英文版详见 [`geo-routing-en.md`](geo-routing-en.md)。
