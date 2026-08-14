# Beauty Production Case Study

> Fill in each section below to document a production deployment of the Beauty microservice framework.
> Remove placeholder text and instructional comments before publishing.

---

## Metadata

| Field | Value |
|-------|-------|
| **Organization** | `[Company or team name]` |
| **Service name** | `[e.g. order-api, payment-gateway]` |
| **Author(s)** | `[Name, role]` |
| **Date** | `[YYYY-MM-DD]` |
| **Beauty version** | `[e.g. v0.x.y]` |
| **Environment** | `[production / staging]` |

---

## Service Overview

### What does the service do?

`[Describe the business purpose in 2-4 sentences. What problem does it solve? Who are the consumers?]`

### Scale

| Metric | Value |
|--------|-------|
| **Requests per second (peak)** | `[e.g. 12,000 RPS]` |
| **Requests per second (average)** | `[e.g. 3,500 RPS]` |
| **Instance count** | `[e.g. 24 pods across 3 AZs]` |
| **Data volume** | `[e.g. 2 TB/day ingress, 500 GB/day egress]` |
| **Latency target (p99)** | `[e.g. < 150 ms]` |
| **Availability SLA** | `[e.g. 99.95%]` |

### Team and timeline

`[Brief note on team size, when Beauty was adopted, and migration timeline if applicable.]`

---

## Architecture

### High-level diagram

`[Insert architecture diagram or describe the topology: ingress, services, registries, databases, message queues.]`

```
[Optional ASCII diagram]

                    ┌─────────────┐
                    │  API Gateway │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ Service A │ │ Service B │ │ Service C │
        └──────────┘ └──────────┘ └──────────┘
```

### Beauty components used

Check all that apply and add brief notes on how each was used:

| Component | Used | Notes |
|-----------|------|-------|
| `beauty.New()` lifecycle | `[ ]` | `[e.g. single binary, 3 services]` |
| `webserver` (HTTP) | `[ ]` | `[e.g. REST API on :8080]` |
| `grpcserver` (gRPC) | `[ ]` | `[e.g. internal RPC on :9090]` |
| Service discovery | `[ ]` | `[e.g. Nacos / etcd / Consul / K8s]` |
| `grpcclient` | `[ ]` | `[e.g. p2c_ewma load balancer]` |
| Middleware (auth, ratelimit, etc.) | `[ ]` | `[list which]` |
| OpenTelemetry (trace/metric) | `[ ]` | `[e.g. Prometheus + Jaeger]` |
| Cron / scheduled jobs | `[ ]` | |
| Message queue (`pkg/mq`) | `[ ]` | `[e.g. Kafka via contrib/kafka]` |
| Distributed lock (`pkg/dlock`) | `[ ]` | |
| Other `pkg/*` or `contrib/*` | `[ ]` | `[specify]` |

### Infrastructure

| Layer | Technology |
|-------|------------|
| **Container orchestration** | `[e.g. Kubernetes 1.29]` |
| **Registry / discovery** | `[e.g. Nacos 2.x]` |
| **Observability** | `[e.g. Prometheus, Grafana, OTel Collector]` |
| **CI/CD** | `[e.g. GitHub Actions, ArgoCD]` |

---

## Why Beauty Was Chosen

### Previous approach

`[What did you use before? e.g. raw net/http + manual wiring, another framework, Spring Boot microservices.]`

### Evaluation criteria

`[List the requirements that mattered: graceful shutdown, service discovery, middleware composition, observability, team Go expertise, etc.]`

### Decision factors

`[Why Beauty over alternatives? Be specific: lifecycle model, module boundaries, contrib isolation, discovery backends, etc.]`

### Trade-offs accepted

`[Any limitations or compromises you knowingly accepted.]`

---

## Performance Results

### Before vs after (if applicable)

| Metric | Before | After (Beauty) | Change |
|--------|--------|----------------|--------|
| p50 latency | `[ ]` | `[ ]` | `[ ]` |
| p99 latency | `[ ]` | `[ ]` | `[ ]` |
| Error rate | `[ ]` | `[ ]` | `[ ]` |
| Memory per instance | `[ ]` | `[ ]` | `[ ]` |
| Cold start time | `[ ]` | `[ ]` | `[ ]` |
| Deploy frequency | `[ ]` | `[ ]` | `[ ]` |

### Load test summary

`[Describe load test setup: tool, duration, concurrency. Include key numbers.]`

### Resource utilization

`[CPU, memory, network under peak load. Any autoscaling behavior observed.]`

### Observability highlights

`[Notable trace spans, metrics dashboards, or alerts that proved useful in production.]`

---

## Lessons Learned

### What worked well

1. `[e.g. Composing middleware via webserver.WithMiddleware reduced boilerplate.]`
2. `[ ]`
3. `[ ]`

### What was challenging

1. `[e.g. etcd key format required documentation for non-Beauty clients.]`
2. `[ ]`
3. `[ ]`

### Recommendations for others

1. `[e.g. Use Nacos for cross-language service discovery.]`
2. `[ ]`
3. `[ ]`

### Would you choose Beauty again?

`[Yes/no and why, in 2-3 sentences.]`

---

## Code Snippets: Key Integration Patterns

Replace the examples below with real code from your deployment (redact secrets and internal hostnames).

### Application bootstrap

```go
// [Describe: how you assemble the app — services, components, discovery]
package main

import (
    "context"

    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/service/grpcserver"
    "github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
    ctx := context.Background()

    app := beauty.New(
        // TODO: replace with your actual options
        beauty.WithService(webserver.New(":8080", httpHandler,
            webserver.WithServiceName("[service-name]"),
        )),
        beauty.WithService(grpcserver.New(":9090", grpcRegister,
            grpcserver.WithServiceName("[service-name]"),
            // grpcserver.WithAutoServiceDiscovery(registry),
        )),
        // beauty.WithTrace(...),
        // beauty.WithMetric(...),
    )

    if err := app.Start(ctx); err != nil {
        panic(err)
    }
}
```

### Service discovery and client dialing

```go
// [Describe: how clients discover and call downstream services]
import "github.com/rushteam/beauty/pkg/client/grpcclient"

func dialDownstream(ctx context.Context) (*grpc.ClientConn, error) {
    return grpcclient.DialContext(ctx, "[registry-url]/[protobuf.service.Name]",
        // grpcclient.WithRegistry(registry),
        // grpcclient.WithLoadBalancer("p2c_ewma"),
    )
}
```

### Middleware chain

```go
// [Describe: your HTTP middleware order and why]
import (
    "github.com/rushteam/beauty/pkg/middleware/recovery"
    "github.com/rushteam/beauty/pkg/middleware/auth"
)

webserver.WithMiddleware(recovery.HTTPMiddleware()),
webserver.WithAuth(authMiddleware),
// ...
```

### Graceful shutdown / lifecycle hook

```go
// [Describe: any custom shutdown logic, drain delays, or component Init]
// Example: registering a custom Component via beauty.WithComponent
```

### Observability setup

```go
// [Describe: trace/metric/log configuration]
// Example: beauty.WithTrace(...), beauty.WithMetric(...)
```

---

## Appendix (optional)

### Related documentation

- `[Link to internal runbooks, ADRs, or Beauty docs used]`

### Contact

`[Team channel, email, or maintainer for follow-up questions.]`
