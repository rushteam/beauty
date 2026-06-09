# 内置 HTTP 中间件

本文档介绍 `pkg/middleware/` 下开箱即用的 HTTP 中间件。弹性相关中间件（熔断、限流、超时、认证）参见 [middleware.md](middleware.md)。

## 使用方式

所有中间件均返回标准 `func(http.Handler) http.Handler`，通过 `webserver.WithMiddleware` 挂载：

```go
import (
    "github.com/rushteam/beauty/pkg/middleware/compress"
    "github.com/rushteam/beauty/pkg/middleware/cors"
    "github.com/rushteam/beauty/pkg/middleware/health"
    "github.com/rushteam/beauty/pkg/middleware/recovery"
)

app := beauty.New(
    beauty.WithWebServer(":8080", handler,
        webserver.WithMiddleware(recovery.HTTPMiddleware()),
        webserver.WithMiddleware(cors.Default().Middleware()),
        webserver.WithMiddleware(compress.Middleware(1024)),
        webserver.WithMiddleware(health.Middleware()),
    ),
)
```

---

## Recovery

捕获 handler 中的 panic，返回 500 JSON 响应，同时记录错误日志和调用栈。

```go
// 默认：panic 时打印 slog.Error
webserver.WithMiddleware(recovery.HTTPMiddleware())

// 自定义 panic 处理
webserver.WithMiddleware(recovery.HTTPMiddleware(
    recovery.WithOnPanic(func(ctx context.Context, p any, stack []byte) {
        sentry.CaptureException(fmt.Errorf("%v", p))
        slog.Error("panic", "panic", p, "stack", string(stack))
    }),
))
```

gRPC 同样支持：

```go
grpcserver.WithGrpcServerUnaryInterceptor(recovery.UnaryServerInterceptor())
grpcserver.WithGrpcServerStreamInterceptor(recovery.StreamServerInterceptor())
```

panic 时 gRPC 返回 `codes.Internal`，HTTP 返回：

```json
{"error": "internal server error"}
```

---

## CORS

```go
// 使用默认配置（允许所有来源，常用 method/header）
webserver.WithMiddleware(cors.Default().Middleware())

// 自定义配置
webserver.WithMiddleware((&cors.Config{
    AllowOrigins:     []string{"https://example.com", "https://app.example.com"},
    AllowMethods:     []string{"GET", "POST", "PUT", "DELETE"},
    AllowHeaders:     []string{"Content-Type", "Authorization"},
    ExposeHeaders:    []string{"X-Request-ID"},
    AllowCredentials: true,
    MaxAge:           3600,
}).Middleware())
```

默认配置：

| 字段 | 默认值 |
|------|--------|
| `AllowOrigins` | `["*"]` |
| `AllowMethods` | GET POST PUT PATCH DELETE OPTIONS HEAD |
| `AllowHeaders` | Content-Type Authorization X-Request-ID |
| `AllowCredentials` | false |
| `MaxAge` | 86400 秒 |

> `AllowCredentials: true` 时不能同时设置 `AllowOrigins: ["*"]`，需指定具体域名。

---

## Compress (gzip)

对响应体进行 gzip 压缩，仅压缩可压缩类型（text/\*、application/json 等），客户端不支持时自动跳过。

```go
// minSize：响应体超过该字节数才压缩，0 表示始终压缩
webserver.WithMiddleware(compress.Middleware(1024)) // 超过 1KB 才压缩
webserver.WithMiddleware(compress.Middleware(0))    // 始终压缩
```

支持压缩的 Content-Type：
- `text/*`（text/html、text/plain、text/css 等）
- `application/json`
- `application/xml`
- `application/javascript`

---

## Health

提供 Kubernetes 标准的存活/就绪探针端点，可作为独立 Handler 或中间件使用。

**探针端点**：

| 路径 | 说明 |
|------|------|
| `GET /healthz` | 存活探针，始终返回 200 |
| `GET /readyz` | 就绪探针，所有检查通过才返回 200 |

```go
// 作为中间件（拦截 /healthz 和 /readyz，其他请求透传）
webserver.WithMiddleware(health.Middleware(
    // 可选：添加就绪检查函数，任意一个返回 error 则 /readyz 返回 503
    func() error { return db.Ping() },
    func() error { return cache.Ping() },
))

// 作为独立 Handler（挂载到指定路由）
mux.Handle("/healthz", health.Handler())
mux.Handle("/readyz", health.Handler(
    func() error { return db.Ping() },
))
```

响应格式：

```json
// 200 OK
{"status": "ok"}

// 503 Service Unavailable
{"status": "error", "error": "dial tcp: connection refused"}
```

---

## Metrics

HTTP / gRPC 请求指标（请求数、耗时直方图、在途请求数）由框架内置的 OpenTelemetry instrumentation 自动产出，**无需额外中间件**：

- HTTP server 默认包裹 `otelhttp.NewHandler(...)`
- HTTP client 默认使用 `otelhttp.NewTransport(...)`
- gRPC server 默认挂载 `otelgrpc.NewServerHandler()`
- gRPC client 默认挂载 `otelgrpc.NewClientHandler()`

这些 instrumentation 会按 OTel 语义约定上报标准指标（如 `http.server.request.duration`、`rpc.server.duration` 等），指标名与标签遵循上游约定，随版本演进，不在框架内重复定义。

只需配合 `beauty.WithMetric(...)` 初始化 OTel MeterProvider，指标即可实际上报：

```go
metricExporter, _ := prometheus.New()
app := beauty.New(
    beauty.WithMetric(telemetry.WithMetricReader(metricExporter)),
    // ...
)
```

---

## 中间件推荐顺序

```go
webserver.WithMiddleware(recovery.HTTPMiddleware()),   // 1. 最外层兜底 panic
webserver.WithMiddleware(cors.Default().Middleware()), // 2. CORS（OPTIONS 提前返回）
webserver.WithMiddleware(health.Middleware()),         // 3. 健康检查（短路，不走后续）
webserver.WithMiddleware(compress.Middleware(1024)),   // 4. 压缩（最内层，压缩最终响应）
```
