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

## AntiReplay（防重放）

基于 nonce + `kvstore.Store.SetNX` 的防重放中间件。每个请求携带唯一 nonce，重复提交会被拒绝。

```go
import (
    "github.com/rushteam/beauty/pkg/middleware/antireplay"
    "github.com/rushteam/beauty/pkg/kvstore"
)

store := redis.NewStore(redisClient) // 或 kvstore.NewMemory()

webserver.WithMiddleware(antireplay.HTTPMiddleware(store))

// 自定义选项
webserver.WithMiddleware(antireplay.HTTPMiddleware(store,
    antireplay.WithHeader("X-Request-Nonce"),        // 默认 X-Nonce
    antireplay.WithKeyPrefix("replay:"),              // 默认 nonce:
    antireplay.WithTTL(5*time.Minute),                // 默认 10 分钟
    antireplay.WithSkipPrefixes("/healthz", "/callback/"),
))
```

---

## SignVerify（HMAC 签名校验）

基于 HMAC-SHA256 的请求签名校验。签名公式：`hex(HMAC-SHA256(secret, timestamp + userID + body))`。

```go
import "github.com/rushteam/beauty/pkg/middleware/signverify"

getSecret := func(appID string) ([]byte, bool) {
    return secrets[appID]  // 根据 X-App-Id 查找 secret
}

webserver.WithMiddleware(signverify.HTTPMiddleware(getSecret))

// 签名校验 + 自动提取用户身份到 auth.User context
webserver.WithMiddleware(signverify.HTTPMiddleware(getSecret,
    signverify.WithExtractUser(),                     // 将 X-User-Id 写入 auth.User
    signverify.WithMaxAge(3*time.Minute),             // 默认 5 分钟
    signverify.WithSkipPrefixes("/healthz", "/callback/"),
))

// 自定义所有 header 名称
signverify.WithAppIDHeader("App-Key")       // 默认 X-App-Id
signverify.WithSignHeader("Signature")      // 默认 X-Sign
signverify.WithTimestampHeader("Req-Time")  // 默认 X-Timestamp
signverify.WithUserIDHeader("Caller-Id")    // 默认 X-User-Id
```

客户端计算签名（`Sign` 函数已公开导出）：

```go
sig := signverify.Sign(secret, timestamp, userID, bodyBytes)
```

---

## TrustedHeaderAuthenticator（信任网关认证）

适用于 API 网关已完成认证、后端服务信任 header 的场景。配合 `signverify` 中间件可防止 header 伪造。

```go
import "github.com/rushteam/beauty/pkg/middleware/auth"

// 信任 X-User-Id header 作为用户身份
authMW := auth.NewAuthMiddleware(auth.Config{
    TokenExtractor: auth.NewHeaderTokenExtractor("X-User-Id", ""),
    Authenticator:  auth.NewTrustedHeaderAuthenticator(),
    SkipPaths:      []string{"/healthz", "/readyz"},
})
webserver.WithMiddleware(auth.HTTPMiddleware(authMW))

// 下游读取用户
user, ok := auth.GetUserFromContext(ctx)
```

---

## 中间件推荐顺序

```go
webserver.WithMiddleware(recovery.HTTPMiddleware()),            // 1. 兜底 panic
webserver.WithMiddleware(cors.Default().Middleware()),          // 2. CORS
webserver.WithMiddleware(health.Middleware()),                  // 3. 健康检查短路
webserver.WithMiddleware(antireplay.HTTPMiddleware(store)),     // 4. 防重放
webserver.WithMiddleware(signverify.HTTPMiddleware(getSecret,   // 5. 签名校验
    signverify.WithExtractUser(),
)),
webserver.WithMiddleware(compress.Middleware(1024)),            // 6. 压缩
```
