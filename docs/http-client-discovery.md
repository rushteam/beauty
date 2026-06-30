# HTTP 客户端服务发现

## 概述

`pkg/client/http` 提供基于服务发现的 HTTP 客户端,对齐 gRPC 的 `ServiceDiscoveryClient`。调用方只需提供**服务名 + 相对路径**,实例选择、URL 拼接、重试换节点均由客户端处理。复用 `pkg/loadbalance`(轮询 / 平滑加权轮询)+ `pkg/service/discover` + `pkg/utils/selector`。

核心是 `discoveryTransport`(实现 `http.RoundTripper`):它把请求的 `URL.Host` 改写为从服务发现选出的实例地址,转发给底层 transport(otelhttp 包装)。这意味着调用方拿到的是**标准 `*http.Client`**,所有 http 生态(otel trace、cookie、自定义 timeout、中间件)都能透明组合。

## 功能特点

- **透明路由**:只需相对路径,transport 自动改写 URL 指向选中实例
- **负载均衡**:轮询(RR)、平滑加权轮询(WRR,nginx SWRR)、随机
- **重试换节点**:5xx / 网络错误触发,指数退避 + ±25% jitter,可配置重试时是否换节点
- **标签过滤**:地域 / 版本 / zone 过滤(fail-closed,无匹配返回空而非全量)
- **服务监听**:实时监听服务变化,自动重建负载均衡器
- **两种用法**:薄包装层(`ServiceDiscoveryHTTPClient`)或裸 `RoundTripper`(塞进已有 `http.Client`)

## 两种用法

### 用法一:薄包装层(推荐)

`ServiceDiscoveryHTTPClient` 封装了 `*http.Client`,提供 `Do` / `DoWith` / `NewRequest` 便捷方法。

```go
package main

import (
    "context"
    "io"
    "log"
    "net/http"

    httpclient "github.com/rushteam/beauty/pkg/client/http"
    "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
)

func main() {
    discovery := etcdv3.NewRegistry(&etcdv3.Config{
        Endpoints: []string{"127.0.0.1:2379"},
        Prefix:    "/beauty",
    })

    cli := httpclient.NewServiceDiscoveryHTTPClient(discovery, "order-svc",
        httpclient.WithHTTPStrategy(httpclient.HTTPWeightedRoundRobin),
        httpclient.WithHTTPMaxRetries(2),
        httpclient.WithHTTPRetryDelay(time.Second),
    )

    ctx := context.Background()
    if err := cli.Start(ctx); err != nil { // 启动 watch
        log.Fatal(err)
    }
    defer cli.Stop()

    // 便捷形式:一步到位
    resp, err := cli.DoWith(ctx, http.MethodGet, "/api/orders/123", nil)
    if err != nil {
        log.Fatal(err)
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    log.Println(string(body))
}
```

### 用法二:裸 RoundTripper(嵌入已有 http.Client)

已有 `*http.Client` 管理逻辑(如自定义 transport 链、cookie jar)的场景,可只拿 `RoundTripper`。

```go
transport := httpclient.NewDiscoveryTransport(discovery, "order-svc",
    httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
)
// transport 需手动 Start(否则首次请求 autoStart 但不启动 watch)
if t, ok := transport.(interface{ Start(context.Context) error }); ok {
    _ = t.Start(ctx)
    defer t.(interface{ Stop() }).Stop()
}

client := &http.Client{
    Transport: transport,
    Timeout:   10 * time.Second,
}

req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/orders/123", nil)
resp, err := client.Do(req) // transport 自动改写 URL.Host
```

## 请求方式

### 便捷形式:`DoWith`

选实例 + 拼 URL + 发送,一步到位。适合无特殊 header 需求的简单调用。

```go
// GET,无 body
resp, err := cli.DoWith(ctx, http.MethodGet, "/api/users/123", nil)

// POST,带 body
resp, err := cli.DoWith(ctx, http.MethodPost, "/api/users",
    strings.NewReader(`{"name":"alice"}`))
```

### 灵活形式:`NewRequest` + `Do`

`NewRequest` 只设相对 path(返回的 `*http.Request` 的 host 为空,由 transport 改写),调用方自由设置 header / body 后用 `Do` 发送。

```go
req, _ := cli.NewRequest(ctx, http.MethodPost, "/api/users")
req.Header.Set("Authorization", "Bearer "+token)
req.Header.Set("Content-Type", "application/json")
req.Body = io.NopCloser(strings.NewReader(`{"name":"alice"}`))
// 带 body 重试需设置 GetBody(否则 transport 会缓存 body 以支持重放)
req.GetBody = func() (io.ReadCloser, error) {
    return io.NopCloser(strings.NewReader(`{"name":"alice"}`)), nil
}

resp, err := cli.Do(req)
```

> **重试与 body**:重试时 body 需可重放。GET / DELETE 无 body 不受影响;POST / PUT 若未设置 `req.GetBody`,transport 会读取并缓存整个 body 以支持重放(小 body 场景无碍,**大 body 流式上传建议提供 `GetBody`**,避免一次性缓存。

## 负载均衡策略

```go
// 轮询(默认):atomic 游标,无锁高吞吐,适合节点等价
httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin)

// 平滑加权轮询(nginx SWRR):按权重比例均匀分发,避免低权重节点被连续命中
httpclient.WithHTTPStrategy(httpclient.HTTPWeightedRoundRobin)

// 随机:每次请求 rand 选节点
httpclient.WithHTTPStrategy(httpclient.HTTPRandom)
```

**权重约定**:从 `ServiceInfo.Metadata["weight"]` 解析(默认 100)。服务端注册时设置:

```go
// webserver 端注册时写 weight
webserver.WithWeight(200)

// 或在 discover.ServiceInfo.Metadata 里直接写
svc.Metadata["weight"] = "200"
```

**scheme 约定**:从 `Metadata["scheme"]` 读(默认 `http`)。HTTPS 后端设置 `Metadata["scheme"] = "https"`。

## 重试配置

```go
cli := httpclient.NewServiceDiscoveryHTTPClient(discovery, "order-svc",
    httpclient.WithHTTPMaxRetries(2),              // 额外重试次数(0=不重试,总尝试=maxRetries+1)
    httpclient.WithHTTPRetryDelay(time.Second),    // 指数退避 base(实际 base*2^i ± 25% jitter)
    httpclient.WithHTTPRetryOnDifferentNode(true), // 重试时是否换节点(默认 true)
)
```

**重试规则**:
- **重试**:5xx 服务端错误、网络错误(连接拒绝 / DNS 失败等)
- **不重试**:4xx 客户端错误(参数问题,重试无意义)、`context.Canceled` / `context.DeadlineExceeded`
- **换节点**:`retryOnDiffNode=true`(默认)时,每次重试重新选实例,处理节点彻底不可用(对齐 gRPC failover);`false` 时复用同一 URL,仅对网络抖动有效

**重试耗尽时的返回**:遵守 `http.RoundTripper` 契约——若最后一次拿到了 resp(如 502),返回 `(resp, nil)`,调用方自行判断 `resp.StatusCode`;只有纯网络错误(无 resp)才返回 error。

```go
resp, err := cli.DoWith(ctx, http.MethodGet, "/api/orders", nil)
if err != nil {
    // 纯网络错误(所有重试都连不上)
    return err
}
defer resp.Body.Close()
if resp.StatusCode >= 500 {
    // 5xx 重试耗尽,调用方决定如何处理
}
```

## 标签过滤

直接复用 `pkg/utils/selector.LabelFilter`,支持地域 / 版本 / zone 过滤。**fail-closed**:无匹配实例返回空(报错),不退回全量——避免静默击穿 region / version 隔离。

```go
// 只路由到 v2 实例(灰度)
f := selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2")

// 地域过滤
f := selector.NewLabelFilter().
    WithRegionIn("us-west-1").
    WithZoneIn("us-west-1a").
    WithEnvironmentIn("production")

cli := httpclient.NewServiceDiscoveryHTTPClient(discovery, "order-svc",
    httpclient.WithHTTPLabelFilter(f),
)
```

服务端注册时写 metadata:

```go
webserver.New(":8080", handler,
    webserver.WithVersion("v2"),
    webserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
    webserver.WithEnvironment("production"),
)
```

## 生命周期

```go
cli := httpclient.NewServiceDiscoveryHTTPClient(discovery, "order-svc")

// Start:拉取初始服务列表 + 启动 watch goroutine(幂等)
if err := cli.Start(ctx); err != nil {
    log.Fatal(err)
}
defer cli.Stop() // 停止后台 goroutine(幂等)

// 未调用 Start 时,首次 Do/DoWith/NewRequest 会 autoStart:
// 仅 refresh 一次服务列表(不启动 watch),并打印警告日志。
// 生产环境应显式 Start,否则服务列表不会动态更新。
```

## 配置选项一览

| Option | 作用 | 默认值 |
|---|---|---|
| `WithHTTPStrategy` | 负载均衡策略 | `HTTPRoundRobin` |
| `WithHTTPLabelFilter` | 标签过滤器 | 无(不过滤) |
| `WithHTTPTimeout` | `*http.Client` 超时 | 30s |
| `WithHTTPMaxRetries` | 额外重试次数 | 1 |
| `WithHTTPRetryDelay` | 指数退避 base | 1s |
| `WithHTTPRetryOnDifferentNode` | 重试是否换节点 | true |

## 与 gRPC 客户端的对照

| 能力 | gRPC (`grpcclient`) | HTTP (`client/http`) |
|---|---|---|
| 服务发现 | `discover.Discovery` | 同 |
| 负载算法 | RR / WRR / Random / LeastConnections | RR / WRR / Random(无 LeastConnections) |
| 重试 | `Call()` failover + grpc RetryPolicy(两层) | `Do`/`DoWith` 重试(单层) |
| 健康检查 | 后台查 `conn.GetState()` | 无(HTTP 无 conn 状态,靠 watch 驱动) |
| 连接排空 | `drainTimeout` | 无(transport 连接池自管) |
| trace | otelgrpc | otelhttp |
| 标签过滤 | `ServiceLabelFilter`(薄封装) | `selector.LabelFilter` 直接用 |

**为什么 HTTP 无 LeastConnections**:gRPC 的 `LeastConnections` 依赖 `grpc.ClientConn.GetState()` 查连接状态;HTTP 客户端不维护 conn 池状态(底层 `http.Transport` 管),无法查 in-flight / 连接状态,故不提供该策略。

## 完整示例

见 `examples/http-service-discovery/main.go`:3 个 HTTP 后端(weight 1:2:3)+ 内存服务发现 + WRR 客户端,演示 `DoWith` 便捷调用与 `NewRequest` + `Do` 灵活调用。

```bash
go run ./examples/http-service-discovery
```

输出可见 WRR 一轮 6 次精确分配(backend-0=1 / backend-1=2 / backend-2=3),符合权重 1:2:3。

## 注意事项

1. **显式 Start**:生产环境务必调用 `Start(ctx)`,否则 autoStart 只 refresh 一次,服务列表不会动态更新
2. **body 重放**:POST / PUT 重试需可重放 body,大 body 流建议设置 `req.GetBody`
3. **4xx 不重试**:客户端错误(参数问题)重试无意义,调用方应直接处理
4. **fail-closed**:标签过滤无匹配时返回空,调用方应据空结果报错,不要退回全量
5. **资源清理**:使用完毕调用 `Stop()` / `Close()` 停止后台 goroutine
