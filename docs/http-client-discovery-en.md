# HTTP Client Service Discovery

## Overview

`pkg/client/http` provides an HTTP client with service discovery, aligned with gRPC's `ServiceDiscoveryClient`. Callers only need to provide **service name + relative path**; instance selection, URL construction, and retry with node switching are handled by the client. It reuses `pkg/store/loadbalance` (round robin / smooth weighted round robin) + `pkg/service/discover` + `pkg/utils/selector`.

The core is `discoveryTransport` (implements `http.RoundTripper`): it rewrites the request's `URL.Host` to the address of an instance selected from service discovery, then forwards to the underlying transport (wrapped with otelhttp). This means callers get a **standard `*http.Client`** — all HTTP ecosystem tooling (otel trace, cookies, custom timeouts, middleware) composes transparently.

## Features

- **Transparent routing**: Only relative paths needed; transport automatically rewrites URL to point at the selected instance
- **Load balancing**: Round robin (RR), smooth weighted round robin (WRR, nginx SWRR), random
- **Retry with node switching**: Triggered on 5xx / network errors, exponential backoff + ±25% jitter, configurable whether to switch nodes on retry
- **Label filtering**: Region / version / zone filtering (fail-closed; returns empty on no match rather than all instances)
- **Service watching**: Real-time service change monitoring, automatic load balancer rebuild
- **Two usage patterns**: Thin wrapper (`ServiceDiscoveryHTTPClient`) or bare `RoundTripper` (plug into an existing `http.Client`)

## Two Usage Patterns

### Pattern 1: Thin Wrapper (Recommended)

`ServiceDiscoveryHTTPClient` wraps `*http.Client` and provides convenient `Do` / `DoWith` / `NewRequest` methods.

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
    if err := cli.Start(ctx); err != nil { // Start watch
        log.Fatal(err)
    }
    defer cli.Stop()

    // Convenience form: one-step call
    resp, err := cli.DoWith(ctx, http.MethodGet, "/api/orders/123", nil)
    if err != nil {
        log.Fatal(err)
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    log.Println(string(body))
}
```

### Pattern 2: Bare RoundTripper (Embed in Existing http.Client)

For scenarios where an existing `*http.Client` manages logic (custom transport chain, cookie jar, etc.), use only the `RoundTripper`.

```go
transport := httpclient.NewDiscoveryTransport(discovery, "order-svc",
    httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin),
)
// Transport must be started manually (otherwise first request autoStarts but does not start watch)
if t, ok := transport.(interface{ Start(context.Context) error }); ok {
    _ = t.Start(ctx)
    defer t.(interface{ Stop() }).Stop()
}

client := &http.Client{
    Transport: transport,
    Timeout:   10 * time.Second,
}

req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/orders/123", nil)
resp, err := client.Do(req) // Transport automatically rewrites URL.Host
```

## Request Patterns

### Convenience Form: `DoWith`

Select instance + build URL + send, all in one step. Suitable for simple calls without special header requirements.

```go
// GET, no body
resp, err := cli.DoWith(ctx, http.MethodGet, "/api/users/123", nil)

// POST, with body
resp, err := cli.DoWith(ctx, http.MethodPost, "/api/users",
    strings.NewReader(`{"name":"alice"}`))
```

### Flexible Form: `NewRequest` + `Do`

`NewRequest` only sets the relative path (returned `*http.Request` has empty host; transport rewrites it). Caller freely sets headers / body, then sends via `Do`.

```go
req, _ := cli.NewRequest(ctx, http.MethodPost, "/api/users")
req.Header.Set("Authorization", "Bearer "+token)
req.Header.Set("Content-Type", "application/json")
req.Body = io.NopCloser(strings.NewReader(`{"name":"alice"}`))
// For body retry, set GetBody (otherwise transport caches body to support replay)
req.GetBody = func() (io.ReadCloser, error) {
    return io.NopCloser(strings.NewReader(`{"name":"alice"}`)), nil
}

resp, err := cli.Do(req)
```

> **Retry and body**: Body must be replayable on retry. GET / DELETE without body are unaffected; for POST / PUT, if `req.GetBody` is not set, transport reads and caches the entire body to support replay (fine for small bodies; **for large body streaming uploads, provide `GetBody`** to avoid one-time caching).

## Load Balancing Strategies

```go
// Round robin (default): atomic cursor, lock-free high throughput, suitable for equivalent nodes
httpclient.WithHTTPStrategy(httpclient.HTTPRoundRobin)

// Smooth weighted round robin (nginx SWRR): distribute evenly by weight ratio, avoids consecutive hits on low-weight nodes
httpclient.WithHTTPStrategy(httpclient.HTTPWeightedRoundRobin)

// Random: rand-select node per request
httpclient.WithHTTPStrategy(httpclient.HTTPRandom)
```

**Weight convention**: Parsed from `ServiceInfo.Metadata["weight"]` (default 100). Set on server registration:

```go
// Set weight when registering on webserver side
webserver.WithWeight(200)

// Or write directly in discover.ServiceInfo.Metadata
svc.Metadata["weight"] = "200"
```

**Scheme convention**: Read from `Metadata["scheme"]` (default `http`). For HTTPS backends, set `Metadata["scheme"] = "https"`.

## Retry Configuration

```go
cli := httpclient.NewServiceDiscoveryHTTPClient(discovery, "order-svc",
    httpclient.WithHTTPMaxRetries(2),              // Extra retry count (0=no retry, total attempts=maxRetries+1)
    httpclient.WithHTTPRetryDelay(time.Second),    // Exponential backoff base (actual base*2^i ± 25% jitter)
    httpclient.WithHTTPRetryOnDifferentNode(true), // Whether to switch nodes on retry (default true)
)
```

**Retry rules**:
- **Retry**: 5xx server errors, network errors (connection refused / DNS failure, etc.)
- **No retry**: 4xx client errors (parameter issues; retry is pointless), `context.Canceled` / `context.DeadlineExceeded`
- **Node switching**: When `retryOnDiffNode=true` (default), each retry re-selects an instance, handling completely unavailable nodes (aligned with gRPC failover); when `false`, reuses the same URL, only effective for network jitter

**Return on retry exhaustion**: Follows `http.RoundTripper` contract — if the last attempt got a resp (e.g. 502), returns `(resp, nil)`; caller checks `resp.StatusCode`; only pure network errors (no resp) return error.

```go
resp, err := cli.DoWith(ctx, http.MethodGet, "/api/orders", nil)
if err != nil {
    // Pure network error (all retries failed to connect)
    return err
}
defer resp.Body.Close()
if resp.StatusCode >= 500 {
    // 5xx retries exhausted; caller decides how to handle
}
```

## Label Filtering

Reuses `pkg/utils/selector.LabelFilter` directly, supporting region / version / zone filtering. **Fail-closed**: returns empty (error) when no instances match, does not fall back to all instances — avoids silently breaking region / version isolation.

```go
// Route only to v2 instances (canary)
f := selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2")

// Region filtering
f := selector.NewLabelFilter().
    WithRegionIn("us-west-1").
    WithZoneIn("us-west-1a").
    WithEnvironmentIn("production")

cli := httpclient.NewServiceDiscoveryHTTPClient(discovery, "order-svc",
    httpclient.WithHTTPLabelFilter(f),
)
```

Write metadata on server registration:

```go
webserver.New(":8080", handler,
    webserver.WithVersion("v2"),
    webserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
    webserver.WithEnvironment("production"),
)
```

## Lifecycle

```go
cli := httpclient.NewServiceDiscoveryHTTPClient(discovery, "order-svc")

// Start: fetch initial service list + start watch goroutine (idempotent)
if err := cli.Start(ctx); err != nil {
    log.Fatal(err)
}
defer cli.Stop() // Stop background goroutine (idempotent)

// When Start is not called, first Do/DoWith/NewRequest triggers autoStart:
// Only refreshes service list once (does not start watch), and logs a warning.
// Production environments should call Start explicitly; otherwise service list won't update dynamically.
```

## Configuration Options

| Option | Purpose | Default |
|---|---|---|
| `WithHTTPStrategy` | Load balancing strategy | `HTTPRoundRobin` |
| `WithHTTPLabelFilter` | Label filter | None (no filtering) |
| `WithHTTPTimeout` | `*http.Client` timeout | 30s |
| `WithHTTPMaxRetries` | Extra retry count | 1 |
| `WithHTTPRetryDelay` | Exponential backoff base | 1s |
| `WithHTTPRetryOnDifferentNode` | Switch nodes on retry | true |

## Comparison with gRPC Client

| Capability | gRPC (`grpcclient`) | HTTP (`client/http`) |
|---|---|---|
| Service discovery | `discover.Discovery` | Same |
| Load algorithms | RR / WRR / Random / LeastConnections | RR / WRR / Random (no LeastConnections) |
| Retry | `Call()` failover + grpc RetryPolicy (two layers) | `Do`/`DoWith` retry (single layer) |
| Health checks | Background `conn.GetState()` polling | None (HTTP has no conn state; driven by watch) |
| Connection draining | `drainTimeout` | None (transport connection pool self-managed) |
| Trace | otelgrpc | otelhttp |
| Label filtering | `ServiceLabelFilter` (thin wrapper) | `selector.LabelFilter` used directly |

**Why HTTP has no LeastConnections**: gRPC's `LeastConnections` relies on `grpc.ClientConn.GetState()` to query connection state; the HTTP client does not maintain conn pool state (underlying `http.Transport` manages it), so in-flight / connection state cannot be queried, and this strategy is not provided.

## Complete Example

See `examples/http-service-discovery/main.go`: 3 HTTP backends (weight 1:2:3) + in-memory service discovery + WRR client, demonstrating `DoWith` convenience calls and `NewRequest` + `Do` flexible calls.

```bash
go run ./examples/http-service-discovery
```

Output shows WRR distributing 6 requests per round precisely (backend-0=1 / backend-1=2 / backend-2=3), matching weight ratio 1:2:3.

## Notes

1. **Explicit Start**: Production environments must call `Start(ctx)`; otherwise autoStart only refreshes once and the service list won't update dynamically
2. **Body replay**: POST / PUT retries require replayable body; for large body streaming, set `req.GetBody`
3. **No 4xx retry**: Client errors (parameter issues) are pointless to retry; caller should handle directly
4. **Fail-closed**: Label filtering returns empty on no match; caller should error on empty result, do not fall back to all instances
5. **Resource cleanup**: Call `Stop()` / `Close()` when done to stop background goroutines
