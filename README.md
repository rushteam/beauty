<div align="center">

# Beauty

**One lifecycle for microservices — plus realtime media, and WASM · Agent sandboxes**

HTTP · gRPC · cron · rooms · streams under a single `app.Start(ctx)`.
When you need live media or AI/policy plugins, the same core hosts them.

[![Go Reference](https://pkg.go.dev/badge/github.com/rushteam/beauty.svg)](https://pkg.go.dev/github.com/rushteam/beauty)
[![Go Report Card](https://goreportcard.com/badge/github.com/rushteam/beauty)](https://goreportcard.com/report/github.com/rushteam/beauty)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**English** · [中文](README-CN.md)

</div>

---

Beauty is a small core (`beauty.New(...).Start(ctx)`) that runs any number of services
under one graceful lifecycle — **mechanisms, not policy**: each package solves one problem
and stays out of your business logic. Heavy or optional stacks (GORM, Kafka, LLM, WASM…)
live as **independent modules** under [`contrib/`](contrib); pull only what you use.

Three reasons people pick Beauty:

| Pillar | What you get |
|---|---|
| **Unified lifecycle** | HTTP, gRPC (+ gateway), cron, MQ consumers, and any custom `Service` share one `Start` / graceful shutdown. |
| **Realtime + media** | WebSocket / SSE / fan-out, game loop + AOI/presence, P2P DataChannel (signaling + mesh/star topology + TCP/QUIC/WebRTC transport), plus RTMP → HLS / LL-HLS and WebRTC WHIP/WHEP + SFU — as services, not a separate stack. |
| **WASM · Agent** | wazero sandboxes for HTTP filters, FaaS handlers, OPA/Rego authz, and LLM agent tools/skills — pure Go, no CGo. |

## Highlights

- **Unified lifecycle** — one `app.Start(ctx)` for HTTP, gRPC, cron, and anything implementing `Service`; config/discovery/resilience/observability built in.
- **Realtime + media** — WS/SSE/QUIC, fixed-timestep game loop, spatial AOI & presence; P2P DataChannel with pluggable transport (TCP/QUIC/WebRTC) and topology (mesh/star/client-server/matchmaking); RTMP ingest, HLS / LL-HLS origin, WebRTC WHIP/WHEP + SFU room, multi-stream hub.
- **WASM · Agent** — [`contrib/wasm`](contrib/wasm) (middleware / FaaS-lite router), [`contrib/wasmopa`](contrib/wasmopa) (Rego→wasm authz), [`contrib/wasmagent`](contrib/wasmagent) (sandboxed agent tools); LLM / RAG / MCP in [`contrib/llm`](contrib/llm) · [`contrib/vector`](contrib/vector) · [`contrib/mcp`](contrib/mcp). See [`docs/wasm-roadmap.md`](docs/wasm-roadmap.md).
- **Also included** — config hot reload (nacos/etcd/consul/k8s), service discovery, dlock/leader, rate limit / circuit break / load shedding, transport-agnostic MQ, OpenTelemetry, consistent-hash sharding, and data/search/broker modules in contrib.

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

## Microservices: register · discover · call

The snippet above co-locates services in one process. Across processes, Beauty uses a
registry (etcd / nacos / …) so providers advertise themselves and callers dial by name —
with load balancing, label routing, and retries on the client side.

**Provider** — register on start:

```go
registry := etcdv3.NewRegistry(&etcdv3.Config{
	Endpoints: []string{"127.0.0.1:2379"},
	Prefix:    "/beauty",
	TTL:       10,
})

app := beauty.New(
	beauty.WithRegistry(registry),
	beauty.WithService(grpcserver.New(":9090",
		func(s *grpc.Server) {
			pb.RegisterGreeterServer(s, &greeter{})
		},
		grpcserver.WithServiceName("helloworld.rpc"),
		grpcserver.WithMetadata(map[string]string{"env": "production"}),
	)),
)
app.Start(ctx)
```

**Caller** — discover and invoke (another process / another service):

```go
conn, err := grpcclient.DialContext(ctx, "beauty://helloworld.rpc?env=production",
	grpcclient.WithRegistry(registry),
	grpcclient.WithLoadBalancer("p2c_ewma"),
)
if err != nil {
	return err
}
defer conn.Close()

client := pb.NewGreeterClient(conn)
resp, err := client.SayHello(ctx, &pb.HelloRequest{Name: "beauty"})
```

Or dial with an embedded registry URL (no separate `Registry` value):

```go
// etcd://host:port/serviceName  or  nacos://host:port/serviceName?...
conn, err := grpcclient.DialContext(ctx, "nacos://127.0.0.1:8848/helloworld.rpc")
```

| Piece | Where |
|---|---|
| Register | `beauty.WithRegistry` / `grpcserver.WithAutoServiceDiscovery` |
| Dial | `grpcclient.DialContext` (`beauty://` · `etcd://` · `nacos://`) |
| Route | query labels (`env`/`region`/…), weighted / P2C load balancing |
| HTTP | symmetric API in `pkg/client/http` |

See [`docs/grpc-service-discovery.md`](docs/grpc-service-discovery.md),
[`docs/grpc-dial-context.md`](docs/grpc-dial-context.md), and
[`examples/grpc-service-discovery`](examples/grpc-service-discovery) /
[`examples/grpc-dial-context`](examples/grpc-dial-context).

## Capability map

| Area | Packages |
|---|---|
| Config / hot reload | `pkg/conf` (nacos, etcd, consul, k8s configmap/secret) |
| Service discovery | `pkg/service/discover`, clients `pkg/client/{grpcclient,http}` |
| Distributed lock / leader | `pkg/dlock` (etcd, consul, redis, k8s Lease, PG Advisory Lock); k8s RBAC guide: [`docs/k8s-rbac.md`](docs/k8s-rbac.md) |
| TTL-KV & primitives | `pkg/kvstore` (redis, etcd) → counter / cooldown / idempotency |
| Concurrency | `pkg/syncx` (Map/ForEach, SingleFlight, Batcher, Debounce/Throttle, Future), `pkg/xgo`, `pkg/safe`, `pkg/chanx`, `pkg/keyedmutex` |
| Resilience | `pkg/ratelimit`, `pkg/governance/{circuitbreaker,overloadctrl}`, `pkg/backoff` |
| Realtime | `pkg/ws`, `pkg/sse`, `pkg/stream`, `pkg/quic`, `pkg/gameloop`, `pkg/spatial`, `pkg/presence`, `pkg/p2p` (signaling + topology + transport) |
| Media | `pkg/media/rtmp`, `pkg/hls`, `pkg/media/hlsmux`, `pkg/media/webrtc` (+ `sfu`), `pkg/media` (hub/supervisor/metrics) |
| WASM / Agent | `contrib/wasm` (middleware + FaaS), `contrib/wasmopa` (OPA/Rego), `contrib/wasmagent` (agent tools/skills); see [`docs/wasm-roadmap.md`](docs/wasm-roadmap.md) |
| Messaging | `pkg/mq`, `pkg/eventbus`, `pkg/webhook`, `pkg/delayqueue`, `pkg/scheduler` |
| Consistency | `pkg/saga`, `pkg/txn`, `pkg/idempotency` |
| Observability | `pkg/service/telemetry`, `pkg/service/logger`, `pkg/buildinfo`, `pkg/service/pprof` |
| Scale-out | `pkg/shard` (consistent-hash routing + reverse proxy) |
| Auth | `pkg/middleware/auth` (authn), `pkg/authz` (authz: RBAC + HTTP/gRPC middleware), `pkg/token` |
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
| [`contrib/kafka`](contrib/kafka) | `pkg/mq` Kafka broker (franz-go + kotel) | `…/contrib/kafka` |
| [`contrib/llm`](contrib/llm) | provider-agnostic LLM client (chat/stream/embed, OpenAI/Anthropic/Azure/compatible) | `…/contrib/llm` |
| [`contrib/vector`](contrib/vector) | vector store / RAG semantic search | `…/contrib/vector` |
| [`contrib/mcp`](contrib/mcp) | Model Context Protocol server/client (expose services as AI tools) | `…/contrib/mcp` |
| [`contrib/wasm`](contrib/wasm) | wazero runtime: HTTP middleware, FaaS-lite router, host funcs, pool/cache | `…/contrib/wasm` |
| [`contrib/wasmopa`](contrib/wasmopa) | OPA Rego→wasm policies as `pkg/authz.Enforcer` | `…/contrib/wasmopa` |
| [`contrib/wasmagent`](contrib/wasmagent) | sandboxed agent tools / skills (`ScriptExecutor` + `agent.Tool`) | `…/contrib/wasmagent` |
| [`contrib/p2p-webrtc`](contrib/p2p-webrtc) | WebRTC DataChannel transport for `pkg/p2p` (NAT traversal, browser interop via pion/webrtc) | `…/contrib/p2p-webrtc` |

Prefix each path with `github.com/rushteam/beauty`. See [`contrib/README.md`](contrib/README.md).

## Observability

OpenTelemetry is wired through the framework: traces and metrics via
`pkg/service/telemetry`, logs via `pkg/service/logger` (slog with automatic
`trace_id`/`span_id` injection), and runtime build info via `pkg/buildinfo`.
Configure an exporter once and the media/mq/client layers emit metrics automatically.

## Documentation

- [`docs/`](docs) — configuration, middleware, discovery, logging, realtime, and more.
- [`docs/k8s-rbac.md`](docs/k8s-rbac.md) — k8s RBAC / ServiceAccount setup guide (leader election + config center).
- [`docs/cross-service-interop.md`](docs/cross-service-interop.md) — cross-service interop: how non-Beauty services discover and call Beauty gRPC services.
- [`docs/wasm-roadmap.md`](docs/wasm-roadmap.md) — WASM tiers (runtime, agent, OPA, FaaS).
- [`docs/p2p-transport.md`](docs/p2p-transport.md) — P2P transport selection guide (TCP vs QUIC vs WebRTC).
- [`examples/`](examples) — runnable demos for most features.
- [`CHANGELOG.md`](CHANGELOG.md) — notable changes.
- [`docs/media-validation.md`](docs/media-validation.md) — real-device checklist for the media stack.

## Contributing

Issues and PRs welcome. Please run `go test ./...` (and the relevant `contrib/<m>`
module tests) and `gofmt` before submitting.

## License

MIT — see [LICENSE](LICENSE).
