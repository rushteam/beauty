# Inter-Service Context Propagation

Beauty context propagation operates at two independent layers:

| Layer | Package | What it carries | Protocol |
|----|-----|--------|------|
| **Business metadata** | `pkg/metadata` | Custom fields such as tenant-id, caller, env | Any `x-` prefixed header |
| **OTel trace context** | `pkg/service/telemetry` | traceparent, tracestate, baggage | W3C TraceContext (default) / B3 |

Both layers operate independently without interfering with each other, but both propagate through the same context chain.

---

## Business Metadata

### Core API

```go
import "github.com/rushteam/beauty/pkg/metadata"

// Write to context
md := metadata.New()
md.Set(metadata.KeyTenantID, "tenant-123")
md.Set(metadata.KeyCaller,   "order-service")
md.Set("x-feature-flag",    "v2")           // custom field
ctx = metadata.NewContext(ctx, md)

// Read from context
md := metadata.FromContext(ctx)
tenantID := md.Get(metadata.KeyTenantID)    // "tenant-123"
custom   := md.Get("x-feature-flag")        // "v2"
```

Predefined keys:

| Constant | Header Name | Purpose |
|------|-----------|------|
| `metadata.KeyTenantID`  | `x-tenant-id`  | Multi-tenant scenarios |
| `metadata.KeyCaller`    | `x-caller`     | Calling service name |
| `metadata.KeyEnv`       | `x-env`        | Environment label (prod/staging/dev) |
| `metadata.KeyRequestID` | `x-request-id` | Request ID (shared with requestid middleware) |
| `metadata.KeyUserID`    | `x-user-id`    | Authenticated user ID |

> Only keys with the `x-` prefix are propagated; control headers such as `Content-Type` and `Authorization` are not included in the propagation chain.

### HTTP Server Integration

```go
import "github.com/rushteam/beauty/pkg/metadata/propagation"

// Mount middleware: extract MD from inbound headers → inject into ctx → echo propagated fields in response headers
webserver.WithMiddleware(propagation.HTTPServerMiddleware)
```

### gRPC Server Integration

```go
grpcserver.WithGrpcServerUnaryInterceptor(propagation.GRPCServerUnaryInterceptor)
grpcserver.WithGrpcServerStreamInterceptor(propagation.GRPCServerStreamInterceptor)
```

### Client Propagation

**HTTP client:**

```go
// Option 1: manual (single request)
req, _ := http.NewRequestWithContext(ctx, "POST", url, body)
propagation.HTTPClientInject(ctx, req)
resp, err := http.DefaultClient.Do(req)

// Option 2: resty global middleware
client := resty.New()
client.OnBeforeRequest(func(c *resty.Client, r *resty.Request) error {
    propagation.HTTPClientInject(r.Context(), r.RawRequest)
    return nil
})
```

**gRPC client (automatic propagation, recommended):**

```go
import "github.com/rushteam/beauty/pkg/metadata/propagation"

// Register client interceptors; all subsequent calls propagate automatically
conn, err := grpc.NewClient(addr,
    grpc.WithChainUnaryInterceptor(propagation.GRPCClientUnaryInterceptor),
    grpc.WithChainStreamInterceptor(propagation.GRPCClientStreamInterceptor),
)
```

### Full Call Chain Example

```
API Gateway (HTTP entry)
  ← receives request headers: X-Tenant-ID: t1, X-Caller: gateway
  → propagation.HTTPServerMiddleware extracts into ctx

  → calls Order gRPC service
      ctx = propagation.GRPCClientInject(ctx)
        injects outgoing metadata: x-tenant-id=t1, x-caller=gateway

Order Service (gRPC)
  ← GRPCServerUnaryInterceptor extracts into ctx
  → calls Inventory HTTP service
      propagation.HTTPClientInject(ctx, req)
        injects headers: X-Tenant-Id: t1, X-Caller: gateway
```

---

## OTel Trace Context Propagation

### Current State

Beauty servers (HTTP/gRPC) integrate `otelhttp` / `otelgrpc`, which:
- Extract trace context from inbound requests and restore spans
- Inject trace context into outbound responses

Async MQ uses [`contrib/kafka`](../contrib/kafka) with built-in kotel (Kafka), or opt-in [`pkg/mq/otelmq`](../pkg/mq/otelmq) (InProc/NATS, etc.). See "MQ Async Tracing" below for details.

This depends on correct configuration of the global `TextMapPropagator`. **As long as you call `beauty.WithTrace()`, W3C TraceContext + Baggage are enabled automatically**—no extra setup required.

### W3C TraceContext (default, recommended)

```go
app := beauty.New(
    beauty.WithTrace(
        telemetry.WithTraceExporter(yourExporter),
    ),
    beauty.WithWebServer(":8080", mux),
    beauty.WithGrpcServer(":9090", register),
)
```

Once enabled, the following headers are handled automatically (per the [W3C Trace Context specification](https://www.w3.org/TR/trace-context/)):

| Header | Meaning |
|--------|------|
| `traceparent` | trace-id, span-id, sampling flags |
| `tracestate`  | vendor extension fields |
| `baggage`     | cross-service KV propagation (W3C Baggage spec) |

### Adding B3 Propagation (Zipkin / legacy Jaeger compatibility)

```go
import "go.opentelemetry.io/contrib/propagators/b3"

app := beauty.New(
    beauty.WithTrace(
        telemetry.WithTraceExporter(yourExporter),
        telemetry.WithTracePropagator(b3.New()), // add B3; W3C is still retained
    ),
)
```

B3 Multi-Header format (Zipkin default):

```
X-B3-TraceId: 80f198ee56343ba864fe8b2a57d3eff7
X-B3-ParentSpanId: 05e3ac9a4f6e3b90
X-B3-SpanId: e457b5a2e4d86bd1
X-B3-Sampled: 1
```

B3 Single-Header format (more compact):

```go
import "go.opentelemetry.io/contrib/propagators/b3"

telemetry.WithTracePropagator(
    b3.New(b3.WithInjectEncoding(b3.B3SingleHeader)),
)
```

```
b3: 80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-1-05e3ac9a4f6e3b90
```

### W3C Baggage Propagation

Baggage is OTel's cross-service KV propagation mechanism. It is similar to business metadata but differs in scope:

| Comparison | W3C Baggage | Business metadata |
|--------|------------|---------------|
| Standard | W3C spec | Beauty internal convention |
| Propagation protocol | `baggage` header | Any `x-` header |
| Best for | Integration with OTel ecosystem (Jaeger/Zipkin) | Pure business fields (tenant-id, etc.) |
| Access | `baggage.FromContext(ctx)` | `metadata.FromContext(ctx)` |

```go
import (
    "go.opentelemetry.io/otel/baggage"
)

// Write baggage
b, _ := baggage.New(
    baggage.NewMemberRaw("tenant-id", "t1"),
)
ctx = baggage.ContextWithBaggage(ctx, b)

// Read baggage
b := baggage.FromContext(ctx)
tenantID := b.Member("tenant-id").Value()
```

> **Practical recommendation**: If your system is already integrated with Jaeger/Zipkin and you need business fields visible in the trace UI, use Baggage. If you only need to propagate business fields between microservices, business metadata is simpler and more direct.

### MQ Async Tracing

gRPC / HTTP have `otelgrpc` / `otelhttp` mounted by default, so synchronous call chains are naturally connected. Async MQ depends on the transport:

**Kafka (`contrib/kafka`, based on franz-go)** — ships with official [kotel](https://github.com/twmb/franz-go/tree/master/plugin/kotel) by default, with automatic publish/receive/process spans; no need to wrap with `otelmq`:

```go
pub, _ := kafka.NewPublisher(brokers) // default WithHooks(kotel)
sub := kafka.NewSubscriber(brokers)
```

**InProc / NATS, etc.** — use opt-in [`pkg/mq/otelmq`](../pkg/mq/otelmq):

```go
import "github.com/rushteam/beauty/pkg/mq/otelmq"

pub := otelmq.Publisher(natsPub)
h := mq.Chain(business, otelmq.Trace("order"), mq.Recover())
```

- `otelmq` lives in a subpackage to keep `pkg/mq` free of external dependencies; it is based on `Message.Headers` and is broker-agnostic.
- Do not wrap Kafka with `otelmq` when kotel is already in use—avoid double injection.

### Propagation Protocol Selection

| Scenario | Recommended Approach |
|------|--------|
| Greenfield system, gRPC only | W3C TraceContext (default, no config needed) |
| Jaeger integration (≥ 1.35) | W3C TraceContext (Jaeger supports it) |
| Zipkin or legacy Jaeger compatibility | Add B3 Multi-Header |
| AWS X-Ray integration | Add `go.opentelemetry.io/contrib/propagators/aws/xray` |
| Mixed multi-system environment | `WithTracePropagator` can add multiple propagators; extraction is tried in order |
| Kafka async cross-service | `contrib/kafka` built-in kotel (enabled by default) |
| Non-Kafka MQ | `pkg/mq/otelmq` |

---

## Complete Configuration Example

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
        // OTel trace: W3C (default) + B3 (legacy system compatibility)
        beauty.WithTrace(
            telemetry.WithTraceExporter(yourExporter),
            telemetry.WithTracePropagator(b3.New()),
        ),

        // HTTP server: mount metadata propagation middleware
        beauty.WithWebServer(":8080", mux,
            webserver.WithMiddleware(propagation.HTTPServerMiddleware),
        ),

        // gRPC server: mount metadata propagation interceptors
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

## Relationship with requestid Middleware

The `requestid` middleware and metadata propagation share the same key name `x-request-id`; they work together:

- `requestid.HTTPMiddleware`: generates a new UUID if `X-Request-ID` is absent, writes to a dedicated ctx key
- `propagation.HTTPServerMiddleware`: also writes `x-request-id` into `metadata.MD`, propagating it downstream with MD

Recommended middleware order:

```go
webserver.WithMiddleware(recovery.HTTPMiddleware()),          // 1. catch panics
webserver.WithMiddleware(propagation.HTTPServerMiddleware),  // 2. extract metadata (including x-request-id)
webserver.WithMiddleware(requestid.HTTPMiddleware),          // 3. generate request-id if missing (deduped inside middleware)
webserver.WithMiddleware(accesslog.HTTPMiddleware),          // 4. log (request-id is ready at this point)
```
