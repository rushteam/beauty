# 服务间上下文透传

Beauty 的上下文透传分两层，各司其职：

| 层 | 包 | 传什么 | 协议 |
|----|-----|--------|------|
| **业务 metadata** | `pkg/metadata` | tenant-id、caller、env 等自定义字段 | 任意 `x-` 前缀 header |
| **OTel trace context** | `pkg/service/telemetry` | traceparent、tracestate、baggage | W3C TraceContext（默认）/ B3 |

两层独立运作，互不干扰，但都通过同一个 context 链路传递。

---

## 业务 Metadata

### 核心 API

```go
import "github.com/rushteam/beauty/pkg/metadata"

// 写入 context
md := metadata.New()
md.Set(metadata.KeyTenantID, "tenant-123")
md.Set(metadata.KeyCaller,   "order-service")
md.Set("x-feature-flag",    "v2")           // 自定义字段
ctx = metadata.NewContext(ctx, md)

// 从 context 读取
md := metadata.FromContext(ctx)
tenantID := md.Get(metadata.KeyTenantID)    // "tenant-123"
custom   := md.Get("x-feature-flag")        // "v2"
```

预定义键：

| 常量 | Header 名 | 用途 |
|------|-----------|------|
| `metadata.KeyTenantID`  | `x-tenant-id`  | 多租户场景 |
| `metadata.KeyCaller`    | `x-caller`     | 调用方服务名 |
| `metadata.KeyEnv`       | `x-env`        | 环境标（prod/staging/dev） |
| `metadata.KeyRequestID` | `x-request-id` | 请求 ID（与 requestid 中间件共享） |
| `metadata.KeyUserID`    | `x-user-id`    | 鉴权后的用户 ID |

> 只有 `x-` 前缀的键会被透传，`Content-Type`、`Authorization` 等控制 header 不会进入传播链路。

### HTTP 服务端接入

```go
import "github.com/rushteam/beauty/pkg/metadata/propagation"

// 挂载中间件：从入站 Header 提取 MD → 注入 ctx → 透传字段回写响应 Header
webserver.WithMiddleware(propagation.HTTPServerMiddleware)
```

### gRPC 服务端接入

```go
grpcserver.WithGrpcServerUnaryInterceptor(propagation.GRPCServerUnaryInterceptor)
grpcserver.WithGrpcServerStreamInterceptor(propagation.GRPCServerStreamInterceptor)
```

### 客户端透传

**HTTP 客户端：**

```go
// 方式一：手动（单次请求）
req, _ := http.NewRequestWithContext(ctx, "POST", url, body)
propagation.HTTPClientInject(ctx, req)
resp, err := http.DefaultClient.Do(req)

// 方式二：resty 全局中间件
client := resty.New()
client.OnBeforeRequest(func(c *resty.Client, r *resty.Request) error {
    propagation.HTTPClientInject(r.Context(), r.RawRequest)
    return nil
})
```

**gRPC 客户端（自动透传，推荐）：**

```go
import "github.com/rushteam/beauty/pkg/metadata/propagation"

// 注册客户端拦截器，之后所有调用自动透传
conn, err := grpc.NewClient(addr,
    grpc.WithChainUnaryInterceptor(propagation.GRPCClientUnaryInterceptor),
    grpc.WithChainStreamInterceptor(propagation.GRPCClientStreamInterceptor),
)
```

### 完整调用链示例

```
API Gateway（HTTP 入口）
  ← 收到请求 Header: X-Tenant-ID: t1, X-Caller: gateway
  → propagation.HTTPServerMiddleware 提取到 ctx

  → 调用 Order gRPC 服务
      ctx = propagation.GRPCClientInject(ctx)
        注入 outgoing metadata: x-tenant-id=t1, x-caller=gateway

Order Service（gRPC）
  ← GRPCServerUnaryInterceptor 提取到 ctx
  → 调用 Inventory HTTP 服务
      propagation.HTTPClientInject(ctx, req)
        注入 Header: X-Tenant-Id: t1, X-Caller: gateway
```

---

## OTel Trace Context 传播

### 当前状态

Beauty 服务端（HTTP/gRPC）已接入 `otelhttp` / `otelgrpc`，它们负责：
- 从入站请求提取 trace context 并恢复 span
- 为出站响应注入 trace context

但这依赖全局 `TextMapPropagator` 的正确配置。**只要调用了 `beauty.WithTrace()`，W3C TraceContext + Baggage 就会自动启用**，无需额外操作。

### W3C TraceContext（默认，推荐）

```go
app := beauty.New(
    beauty.WithTrace(
        telemetry.WithTraceExporter(yourExporter),
    ),
    beauty.WithWebServer(":8080", mux),
    beauty.WithGrpcServer(":9090", register),
)
```

启用后自动处理以下 header（符合 [W3C Trace Context 规范](https://www.w3.org/TR/trace-context/)）：

| Header | 含义 |
|--------|------|
| `traceparent` | trace-id、span-id、采样标志 |
| `tracestate`  | 供应商扩展字段 |
| `baggage`     | 跨服务透传的 KV（W3C Baggage 规范） |

### 追加 B3 传播（兼容 Zipkin / Jaeger 旧版）

```go
import "go.opentelemetry.io/contrib/propagators/b3"

app := beauty.New(
    beauty.WithTrace(
        telemetry.WithTraceExporter(yourExporter),
        telemetry.WithTracePropagator(b3.New()), // 追加 B3，W3C 依然保留
    ),
)
```

B3 Multi-Header 格式（Zipkin 默认）：

```
X-B3-TraceId: 80f198ee56343ba864fe8b2a57d3eff7
X-B3-ParentSpanId: 05e3ac9a4f6e3b90
X-B3-SpanId: e457b5a2e4d86bd1
X-B3-Sampled: 1
```

B3 Single-Header 格式（更紧凑）：

```go
import "go.opentelemetry.io/contrib/propagators/b3"

telemetry.WithTracePropagator(
    b3.New(b3.WithInjectEncoding(b3.B3SingleHeader)),
)
```

```
b3: 80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-1-05e3ac9a4f6e3b90
```

### W3C Baggage 透传

Baggage 是 OTel 提供的跨服务 KV 透传机制，与业务 metadata 定位相似但有区别：

| 对比项 | W3C Baggage | 业务 metadata |
|--------|------------|---------------|
| 标准 | W3C 规范 | Beauty 内部约定 |
| 传播协议 | `baggage` header | 任意 `x-` header |
| 适合场景 | 与 OTel 生态（Jaeger/Zipkin）集成 | 纯业务字段（tenant-id 等） |
| 访问方式 | `baggage.FromContext(ctx)` | `metadata.FromContext(ctx)` |

```go
import (
    "go.opentelemetry.io/otel/baggage"
)

// 写入 baggage
b, _ := baggage.New(
    baggage.NewMemberRaw("tenant-id", "t1"),
)
ctx = baggage.ContextWithBaggage(ctx, b)

// 读取 baggage
b := baggage.FromContext(ctx)
tenantID := b.Member("tenant-id").Value()
```

> **实践建议**：如果你的系统已经接入 Jaeger/Zipkin 并需要在 trace 界面里看到业务字段，用 Baggage；如果只是微服务间透传业务字段，用业务 metadata 更简单直接。

### 传播协议选型建议

| 场景 | 推荐方案 |
|------|--------|
| 全新系统，只用 gRPC | W3C TraceContext（默认，无需配置）|
| 接入 Jaeger（新版 ≥ 1.35） | W3C TraceContext（Jaeger 已支持）|
| 兼容 Zipkin 或老版 Jaeger | 追加 B3 Multi-Header |
| 与 AWS X-Ray 集成 | 追加 `go.opentelemetry.io/contrib/propagators/aws/xray` |
| 多系统混合 | `WithTracePropagator` 可追加多个，按顺序尝试提取 |

---

## 完整配置示例

```go
package main

import (
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/metadata/propagation"
    "github.com/rushteam/beauty/pkg/service/telemetry"
    "github.com/rushteam/beauty/pkg/service/grpcserver"
    "github.com/rushteam/beauty/pkg/service/webserver"
    "go.opentelemetry.io/contrib/propagators/b3"
)

func main() {
    app := beauty.New(
        // OTel trace：W3C（默认）+ B3（兼容旧系统）
        beauty.WithTrace(
            telemetry.WithTraceExporter(yourExporter),
            telemetry.WithTracePropagator(b3.New()),
        ),

        // HTTP 服务：挂载 metadata 透传中间件
        beauty.WithWebServer(":8080", mux,
            webserver.WithMiddleware(propagation.HTTPServerMiddleware),
        ),

        // gRPC 服务：挂载 metadata 透传拦截器
        beauty.WithGrpcServer(":9090", register,
            grpcserver.WithGrpcServerUnaryInterceptor(
                propagation.GRPCServerUnaryInterceptor,
            ),
            grpcserver.WithGrpcServerStreamInterceptor(
                propagation.GRPCServerStreamInterceptor,
            ),
        ),
    )

    app.Start(ctx)
}
```

---

## 与 requestid 中间件的关系

`requestid` 中间件和 metadata 透传使用同一个键名 `x-request-id`，两者协同：

- `requestid.HTTPMiddleware`：若无 `X-Request-ID` 则生成新 UUID，写入 ctx 专用 key
- `propagation.HTTPServerMiddleware`：同时将 `x-request-id` 写入 `metadata.MD`，随 MD 透传到下游

建议的中间件顺序：

```go
webserver.WithMiddleware(recovery.HTTPMiddleware()),          // 1. 兜底 panic
webserver.WithMiddleware(propagation.HTTPServerMiddleware),  // 2. 提取 metadata（含 x-request-id）
webserver.WithMiddleware(requestid.HTTPMiddleware),          // 3. 若无 request-id 则生成（中间件内去重）
webserver.WithMiddleware(accesslog.HTTPMiddleware),          // 4. 记录日志（此时 request-id 已就绪）
```
