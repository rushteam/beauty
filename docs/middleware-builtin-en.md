# Built-in HTTP Middleware

This document describes the out-of-the-box HTTP middleware under `pkg/middleware/`. For resilience middleware (circuit breaker, rate limiting, timeout, authentication), see [middleware.md](middleware.md).

## Usage

All middleware returns a standard `func(http.Handler) http.Handler` and is mounted via `webserver.WithMiddleware`:

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

Captures panics in handlers, returns a 500 JSON response, and logs the error with stack trace.

```go
// Default: slog.Error on panic
webserver.WithMiddleware(recovery.HTTPMiddleware())

// Custom panic handler
webserver.WithMiddleware(recovery.HTTPMiddleware(
    recovery.WithOnPanic(func(ctx context.Context, p any, stack []byte) {
        sentry.CaptureException(fmt.Errorf("%v", p))
        slog.Error("panic", "panic", p, "stack", string(stack))
    }),
))
```

gRPC is also supported:

```go
grpcserver.WithGrpcServerUnaryInterceptor(recovery.UnaryServerInterceptor())
grpcserver.WithGrpcServerStreamInterceptor(recovery.StreamServerInterceptor())
```

On panic, gRPC returns `codes.Internal`; HTTP returns:

```json
{"error": "internal server error"}
```

---

## CORS

```go
// Use default config (allow all origins, common methods/headers)
webserver.WithMiddleware(cors.Default().Middleware())

// Custom config
webserver.WithMiddleware((&cors.Config{
    AllowOrigins:     []string{"https://example.com", "https://app.example.com"},
    AllowMethods:     []string{"GET", "POST", "PUT", "DELETE"},
    AllowHeaders:     []string{"Content-Type", "Authorization"},
    ExposeHeaders:    []string{"X-Request-ID"},
    AllowCredentials: true,
    MaxAge:           3600,
}).Middleware())
```

Default configuration:

| Field | Default |
|------|--------|
| `AllowOrigins` | `["*"]` |
| `AllowMethods` | GET POST PUT PATCH DELETE OPTIONS HEAD |
| `AllowHeaders` | Content-Type Authorization X-Request-ID |
| `AllowCredentials` | false |
| `MaxAge` | 86400 seconds |

> When `AllowCredentials: true`, you cannot set `AllowOrigins: ["*"]`; specify concrete domains instead.

---

## Compress (gzip)

Gzip-compresses response bodies. Only compressible types (text/\*, application/json, etc.) are compressed; skipped automatically when the client does not support gzip.

```go
// minSize: compress only when response body exceeds this many bytes; 0 means always compress
webserver.WithMiddleware(compress.Middleware(1024)) // compress when over 1KB
webserver.WithMiddleware(compress.Middleware(0))    // always compress
```

Supported Content-Types for compression:
- `text/*` (text/html, text/plain, text/css, etc.)
- `application/json`
- `application/xml`
- `application/javascript`

---

## Health

Provides Kubernetes-standard liveness and readiness probe endpoints. Can be used as a standalone handler or as middleware.

**Probe endpoints**:

| Path | Description |
|------|------|
| `GET /healthz` | Liveness probe; always returns 200 |
| `GET /readyz` | Readiness probe; returns 200 only when all checks pass |

```go
// As middleware (handles /healthz and /readyz; other requests pass through)
webserver.WithMiddleware(health.Middleware(
    // Optional: add readiness check functions; /readyz returns 503 if any returns error
    func() error { return db.Ping() },
    func() error { return cache.Ping() },
))

// As standalone handler (mounted on specific routes)
mux.Handle("/healthz", health.Handler())
mux.Handle("/readyz", health.Handler(
    func() error { return db.Ping() },
))
```

Response format:

```json
// 200 OK
{"status": "ok"}

// 503 Service Unavailable
{"status": "error", "error": "dial tcp: connection refused"}
```

---

## Metrics

HTTP and gRPC request metrics (request count, duration histogram, in-flight requests) are produced automatically by the framework's built-in OpenTelemetry instrumentation. **No additional middleware is required**:

- HTTP server is wrapped with `otelhttp.NewHandler(...)` by default
- HTTP client uses `otelhttp.NewTransport(...)` by default
- gRPC server mounts `otelgrpc.NewServerHandler()` by default
- gRPC client mounts `otelgrpc.NewClientHandler()` by default

These instrumentations report standard metrics per OTel semantic conventions (e.g. `http.server.request.duration`, `rpc.server.duration`). Metric names and labels follow upstream conventions and evolve with versions; they are not redefined inside the framework.

Initialize an OTel MeterProvider with `beauty.WithMetric(...)` and metrics will be exported:

```go
metricExporter, _ := prometheus.New()
app := beauty.New(
    beauty.WithMetric(telemetry.WithMetricReader(metricExporter)),
    // ...
)
```

---

## AntiReplay

Nonce-based anti-replay middleware using `kvstore.Store.SetNX`. Each request carries a unique nonce; duplicate submissions are rejected.

```go
import (
    "github.com/rushteam/beauty/pkg/middleware/antireplay"
    "github.com/rushteam/beauty/pkg/kvstore"
)

store := redis.NewStore(redisClient) // or kvstore.NewMemory()

webserver.WithMiddleware(antireplay.HTTPMiddleware(store))

// Custom options
webserver.WithMiddleware(antireplay.HTTPMiddleware(store,
    antireplay.WithHeader("X-Request-Nonce"),        // default X-Nonce
    antireplay.WithKeyPrefix("replay:"),              // default nonce:
    antireplay.WithTTL(5*time.Minute),                // default 10 minutes
    antireplay.WithSkipPrefixes("/healthz", "/callback/"),
))
```

---

## SignVerify (HMAC Signature Verification)

HMAC-SHA256 request signature verification. Signature formula: `hex(HMAC-SHA256(secret, timestamp + userID + body))`.

```go
import "github.com/rushteam/beauty/pkg/middleware/signverify"

getSecret := func(appID string) ([]byte, bool) {
    return secrets[appID]  // look up secret by X-App-Id
}

webserver.WithMiddleware(signverify.HTTPMiddleware(getSecret))

// Signature verification + automatic user identity extraction into auth.User context
webserver.WithMiddleware(signverify.HTTPMiddleware(getSecret,
    signverify.WithExtractUser(),                     // write X-User-Id into auth.User
    signverify.WithMaxAge(3*time.Minute),             // default 5 minutes
    signverify.WithSkipPrefixes("/healthz", "/callback/"),
))

// Customize all header names
signverify.WithAppIDHeader("App-Key")       // default X-App-Id
signverify.WithSignHeader("Signature")      // default X-Sign
signverify.WithTimestampHeader("Req-Time")  // default X-Timestamp
signverify.WithUserIDHeader("Caller-Id")    // default X-User-Id
```

Client-side signature computation (`Sign` is exported):

```go
sig := signverify.Sign(secret, timestamp, userID, bodyBytes)
```

---

## TrustedHeaderAuthenticator (Gateway-Trusted Authentication)

For scenarios where an API gateway has already authenticated the request and backend services trust headers. Use together with the `signverify` middleware to prevent header forgery.

```go
import "github.com/rushteam/beauty/pkg/middleware/auth"

// Trust X-User-Id header as user identity
authMW := auth.NewAuthMiddleware(auth.Config{
    TokenExtractor: auth.NewHeaderTokenExtractor("X-User-Id", ""),
    Authenticator:  auth.NewTrustedHeaderAuthenticator(),
    SkipPaths:      []string{"/healthz", "/readyz"},
})
webserver.WithMiddleware(auth.HTTPMiddleware(authMW))

// Read user downstream
user, ok := auth.GetUserFromContext(ctx)
```

---

## Recommended Middleware Order

```go
webserver.WithMiddleware(recovery.HTTPMiddleware()),            // 1. catch panics
webserver.WithMiddleware(cors.Default().Middleware()),          // 2. CORS
webserver.WithMiddleware(health.Middleware()),                  // 3. health check short-circuit
webserver.WithMiddleware(antireplay.HTTPMiddleware(store)),     // 4. anti-replay
webserver.WithMiddleware(signverify.HTTPMiddleware(getSecret,   // 5. signature verification
    signverify.WithExtractUser(),
)),
webserver.WithMiddleware(compress.Middleware(1024)),            // 6. compression
```
