# Beauty

> A batteries-included Go microservices framework — compose HTTP / gRPC / cron / realtime / media services under one lifecycle, with config, discovery, resilience, messaging and observability built in.

English | [中文](README-CN.md)

Beauty gives you a small core (`beauty.New(...).Start(ctx)`) that runs any number of
services under a single graceful lifecycle, plus a broad set of **mechanisms, not policy**:
each package solves one problem and stays out of your business logic. Heavy or optional
integrations (GORM, Kafka, LLM, …) live as **independent modules** under [`contrib/`](contrib)
so you only pull what you use.

## Highlights

- **Unified services** — HTTP, gRPC (+ gateway), cron, and any custom `Service` under one `app.Start(ctx)` with graceful shutdown.
- **Config & discovery** — config center with hot reload (nacos/etcd/consul/k8s), service discovery, distributed lock / leader election, TTL-KV.
- **Resilience** — rate limiting, circuit breaking, load shedding, backoff; retry + circuit breaker wired into the HTTP/gRPC clients.
- **Realtime** — WebSocket / SSE / fan-out broadcaster, QUIC transport, a fixed-timestep game loop, spatial AOI & presence.
- **Media** — RTMP ingest, HLS / LL-HLS origin, WebRTC WHIP/WHEP and an SFU room, multi-stream hub.
- **Messaging** — a transport-agnostic message-queue abstraction with a consumer-as-Service, plus broker bindings in contrib.
- **Observability** — OpenTelemetry traces & metrics, slog logging with trace correlation, build info, pprof.
- **Scale-out** — consistent-hash sharding to route stateful services (rooms/streams/sessions) across replicas.
- **contrib modules** — data (GORM, database/sql + sqlc), search, MQ brokers (NATS/JetStream/Kafka), and **AI** (LLM clients, vector/RAG, MCP).

## Install

```bash
# Library
go get github.com/rushteam/beauty

# CLI (scaffolding, code-gen, dev hot-reload)
go install github.com/rushteam/beauty/tools/cmd/beauty@latest
```

## Quick start

Minimal service:

```go
package main

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from beauty"))
	})

	app := beauty.New(
		beauty.WithWebServer(":8080", mux, beauty.WithServiceName("hello")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

Scaffold a full project with the CLI:

```bash
beauty new my-service   # generate project (layout, Makefile, Docker/k8s optional)
cd my-service && go run .
```

## Composing services

`beauty.New` takes any number of services; each implements a tiny `Service` interface
(`Start(ctx) error` + `String() string`) and shuts down gracefully together:

```go
app := beauty.New(
	beauty.WithWebServer(":8080", mux, beauty.WithServiceName("api")),
	beauty.WithGrpcServer(":9090", func(s *grpc.Server) {
		pb.RegisterGreeterServer(s, &greeter{})
	}, beauty.WithServiceName("grpc")),
	beauty.WithService(myCustomService), // anything with Start/String
)
app.Start(ctx) // blocks until signal; drains all services
```

- **HTTP** — bring any `http.Handler` (chi/gin/net-http).
- **gRPC** — register your servers; standard health service + retry policy included. REST gateway via `pkg/service/grpcgw`.
- **Cron** — scheduled jobs that run only on the elected leader.

## Capability map

| Area | Packages |
|---|---|
| Config / hot reload | `pkg/conf` (nacos, etcd, consul, k8s configmap/secret) |
| Service discovery | `pkg/service/discover`, clients `pkg/client/{grpcclient,http}` |
| Distributed lock / leader | `pkg/dlock` (etcd, consul, redis, k8s) |
| TTL-KV & primitives | `pkg/kvstore` (redis, etcd) → counter / cooldown / idempotency |
| Resilience | `pkg/ratelimit`, `pkg/governance/{circuitbreaker,overloadctrl}`, `pkg/backoff` |
| Realtime | `pkg/ws`, `pkg/sse`, `pkg/stream`, `pkg/quic`, `pkg/gameloop`, `pkg/spatial`, `pkg/presence` |
| Media | `pkg/media/rtmp`, `pkg/hls`, `pkg/media/hlsmux`, `pkg/media/webrtc` (+ `sfu`), `pkg/media` (hub/supervisor/metrics) |
| Messaging | `pkg/mq`, `pkg/eventbus`, `pkg/webhook`, `pkg/delayqueue`, `pkg/scheduler` |
| Consistency | `pkg/saga`, `pkg/txn`, `pkg/idempotency` |
| Observability | `pkg/service/telemetry`, `pkg/service/logger`, `pkg/buildinfo`, `pkg/service/pprof` |
| Scale-out | `pkg/shard` (consistent-hash routing + reverse proxy) |
| Auth | `pkg/middleware/auth`, `pkg/token` |
| Domain / game | `pkg/{leaderboard,matchmaker,leveling,questlog,versus,tally,reddot,...}` |

See [`docs/`](docs) and [`examples/`](examples) for details and runnable demos.

## Messaging

A transport-agnostic queue (`pkg/mq`): `Publisher`/`Subscriber` interfaces + a
`Consumer` that runs as a `beauty.Service`, plus `Chain`/`Retry`/`Recover` middleware.
An in-process implementation ships in core; real brokers are opt-in contrib modules.

```go
consumer := mq.NewConsumer(broker).
	Handle("orders", handle, mq.WithGroup("order-workers"))
app := beauty.New(beauty.WithService(consumer))
```

## contrib modules

Heavy / optional integrations are **separate Go modules** (own `go.mod`, tagged
independently) — import only what you need; the core dependency graph stays lean.

| Module | What | `go get` |
|---|---|---|
| [`contrib/gorm`](contrib/gorm) | GORM: read/write split, otel tracing, slog, error mapping | `…/contrib/gorm` |
| [`contrib/sqldb`](contrib/sqldb) | `database/sql` read/write split + otel, pairs with **sqlc** | `…/contrib/sqldb` |
| [`contrib/elasticsearch`](contrib/elasticsearch) | Elasticsearch v8 search / index / health | `…/contrib/elasticsearch` |
| [`contrib/nats`](contrib/nats) | `pkg/mq` NATS broker (at-most-once) | `…/contrib/nats` |
| [`contrib/natsjs`](contrib/natsjs) | `pkg/mq` NATS JetStream (persistent, at-least-once) | `…/contrib/natsjs` |
| [`contrib/kafka`](contrib/kafka) | `pkg/mq` Kafka broker (consumer group) | `…/contrib/kafka` |
| [`contrib/llm`](contrib/llm) | provider-agnostic LLM client (chat/stream/embed, OpenAI/Anthropic/Azure/compatible) | `…/contrib/llm` |
| [`contrib/vector`](contrib/vector) | vector store / RAG semantic search | `…/contrib/vector` |
| [`contrib/mcp`](contrib/mcp) | Model Context Protocol server/client (expose services as AI tools) | `…/contrib/mcp` |

Prefix each path with `github.com/rushteam/beauty`. See [`contrib/README.md`](contrib/README.md).

## Observability

OpenTelemetry is wired through the framework: traces and metrics via
`pkg/service/telemetry`, logs via `pkg/service/logger` (slog with automatic
`trace_id`/`span_id` injection), and runtime build info via `pkg/buildinfo`.
Configure an exporter once and the media/mq/client layers emit metrics automatically.

## Documentation

- [`docs/`](docs) — configuration, middleware, discovery, logging, realtime, and more.
- [`examples/`](examples) — runnable demos for most features.
- [`CHANGELOG.md`](CHANGELOG.md) — notable changes.
- [`docs/media-validation.md`](docs/media-validation.md) — real-device checklist for the media stack.

## Contributing

Issues and PRs welcome. Please run `go test ./...` (and the relevant `contrib/<m>`
module tests) and `gofmt` before submitting.

## License

MIT — see [LICENSE](LICENSE).
