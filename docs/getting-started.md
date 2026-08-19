# Getting Started with Beauty

A 15-minute guide to building microservices with the Beauty Go framework.

---

## What is Beauty?

Beauty is a lightweight Go microservice framework built around a single entry point: `beauty.New(...).Start(ctx)`. You compose HTTP servers, gRPC servers, cron jobs, message consumers, and any custom service into one application that shares a unified lifecycle — one startup, one graceful shutdown. When a signal arrives or any service exits, every component drains and stops in order: deregister from the registry, wait for clients to notice, then stop serving.

The design philosophy is **mechanisms, not policy**. Each package in `pkg/*` solves one infrastructure problem — config reload, service discovery, rate limiting, distributed locks — and stays out of your business logic. You bring your own handlers, protobuf services, and data layers. Heavy or optional integrations (GORM, Kafka, LLM, WASM) live as independent modules under [`contrib/`](../contrib/) with their own `go.mod`; import only what you need and keep the core dependency graph lean.

---

## Prerequisites

- **Go 1.26+**
- **Beauty library:**

```bash
go get github.com/rushteam/beauty
```

- **Beauty CLI** (scaffolding, codegen, dev hot-reload):

```bash
go install github.com/rushteam/beauty/tools/cmd/beauty@latest
beauty new my-service   # scaffold project; cd my-service && go run .
```

---

## Quick Start: Your First Service

```go
package main

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from beauty"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	app := beauty.New(
		beauty.WithWebServer(":8080", mux, webserver.WithServiceName("hello")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

Run: `go run .` then `curl http://localhost:8080/`.

`app.Start(ctx)` blocks until Ctrl+C. Anything implementing `Start(ctx) error` + `String() string` can be added via `beauty.WithService`. Full demo: [examples/complete/example](../examples/complete/example).

---

## Adding gRPC

Compose HTTP and gRPC in one process — each listener is a separate service that shuts down together:

```go
package main

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	pb "your/module/api/v1"
	"google.golang.org/grpc"
)

type greeter struct {
	pb.UnimplementedGreeterServer
}

func (g *greeter) SayHello(_ context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
	return &pb.HelloReply{Message: "hello, " + req.Name}, nil
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("HTTP on :8080"))
	})

	app := beauty.New(
		beauty.WithWebServer(":8080", mux, webserver.WithServiceName("api")),
		beauty.WithGrpcServer(":9090", func(s *grpc.Server) {
			pb.RegisterGreeterServer(s, &greeter{})
		}, grpcserver.WithServiceName("greeter.rpc")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

Beauty includes gRPC health checks and retry policy. REST over gRPC: `pkg/service/grpcgw`. Also available: `beauty.WithCrontab(...)`, `beauty.WithPprof()`, `beauty.WithService(custom)`.

---

## Configuration

`beauty.WithConfig` loads config on startup and watches for changes. Local files and remote centers share the same API via `pkg/conf`.

`config.yaml`:

```yaml
name: my-service
port: 8080
```

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/conf"
)

type AppConfig struct {
	Name string `mapstructure:"name"`
	Port int    `mapstructure:"port"`
}

func main() {
	loader, err := conf.New("config.yaml")
	if err != nil {
		panic(err)
	}

	var current atomic.Pointer[AppConfig]
	mux := http.NewServeMux()
	mux.HandleFunc("/config", func(w http.ResponseWriter, _ *http.Request) {
		if c := current.Load(); c != nil {
			slog.Info("serving", "name", c.Name, "port", c.Port)
		}
		w.WriteHeader(http.StatusOK)
	})

	app := beauty.New(
		beauty.WithConfig(loader, func(c *AppConfig) {
			current.Store(c)
			slog.Info("config (re)loaded", "name", c.Name, "port", c.Port)
		}),
		beauty.WithWebServer(":8080", mux),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

Edit `config.yaml` while running — the callback fires with a fresh struct. Invalid configs are rejected; the previous value is kept.

Remote backends (import the infra package to register the scheme):

```go
import _ "github.com/rushteam/beauty/pkg/infra/etcd"
import _ "github.com/rushteam/beauty/pkg/infra/nacos"
import _ "github.com/rushteam/beauty/pkg/infra/consul"

loader, _ := conf.New("etcd://127.0.0.1:2379/myapp/config.yaml")
loader, _ := conf.New("nacos://127.0.0.1:8848/myapp.yaml?namespace=dev")
loader, _ := conf.New("consul://127.0.0.1:8500/myapp/config")
```

See [configuration.md](configuration.md) and [examples/config](../examples/config).

---

## Middleware Stack

Attach HTTP middleware via `webserver.WithMiddleware` (outermost runs first). Matching gRPC interceptors use `grpcserver.WithGrpcServerUnaryInterceptor`.

```go
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/middleware/ratelimit"
	"github.com/rushteam/beauty/pkg/middleware/timeout"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	authenticator := auth.NewStaticTokenAuthenticator()
	authenticator.AddToken("secret-token", auth.NewUser("1", "alice", []string{"user"}))

	authMW := auth.NewAuthMiddleware(auth.Config{
		TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
		Authenticator:  authenticator,
		SkipPaths:      []string{"/health"},
	})
	rlMW := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
		Rate: 10, Burst: 20, KeyExtractor: ratelimit.NewIPKeyExtractor(),
	})
	tc := timeout.NewTimeoutController(timeout.Config{Timeout: 5 * time.Second})

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("OK")) })
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.GetUserFromContext(r.Context())
		w.Write([]byte("hello, " + user.Name()))
	})

	app := beauty.New(
		beauty.WithService(webserver.New(":8080", mux,
			webserver.WithServiceName("api"),
			webserver.WithMiddleware(
				auth.HTTPMiddleware(authMW),
				ratelimit.HTTPMiddleware(rlMW),
				timeout.HTTPMiddleware(tc),
			),
		)),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

```bash
curl http://localhost:8080/health
curl -H "Authorization: Bearer secret-token" http://localhost:8080/api
```

Full demo with circuit breaker: [examples/security/auth-ratelimit](../examples/security/auth-ratelimit). Reference: [middleware.md](middleware.md).

---

## Service Discovery

Providers register on start; callers dial by service name with load balancing and label routing.

**Provider:**

```go
registry := etcdv3.NewRegistry(&etcdv3.Config{
	Endpoints: []string{"127.0.0.1:2379"},
	Prefix:    "/beauty",
	TTL:       10,
})

app := beauty.New(
	beauty.WithRegistry(registry),
	beauty.WithGrpcServer(":9090", func(s *grpc.Server) {
		pb.RegisterGreeterServer(s, &greeter{})
	},
		grpcserver.WithServiceName("helloworld.rpc"),
		grpcserver.WithMetadata(map[string]string{"env": "production"}),
	),
)
app.Start(ctx)
```

**Caller** (another process):

```go
conn, err := grpcclient.DialContext(ctx,
	"beauty://helloworld.rpc?env=production",
	grpcclient.WithRegistry(registry),
	grpcclient.WithLoadBalancer("p2c_ewma"),
)
defer conn.Close()

client := pb.NewGreeterClient(conn)
resp, err := client.SayHello(ctx, &pb.HelloRequest{Name: "beauty"})
```

Embed the registry in the URL when you don't pass a separate `Registry`:

```go
conn, _ := grpcclient.DialContext(ctx, "etcd://127.0.0.1:2379/helloworld.rpc")
conn, _ := grpcclient.DialContext(ctx, "nacos://127.0.0.1:8848/helloworld.rpc")
```

On shutdown Beauty deregisters first, optionally waits (`beauty.WithShutdownDrainDelay`), then stops the server.

| Concern | API |
|---------|-----|
| Register | `beauty.WithRegistry` + `grpcserver.WithServiceName` |
| Dial | `grpcclient.DialContext` |
| HTTP client | `pkg/client/http` |

> **Gateway compatibility (Higress / Envoy):** When registering to Nacos or Consul, Beauty
> automatically injects `metadata["protocol"]` (`GRPC`/`HTTP`/`HTTPS`) so that
> [Higress](https://higress.io) and other Envoy-based gateways can detect the backend protocol
> without extra annotations. See [grpc-service-discovery.md](grpc-service-discovery.md) for details.

Demos: [examples/grpc-service-discovery](../examples/grpc-service-discovery), [examples/grpc-dial-context](../examples/grpc-dial-context). Docs: [grpc-service-discovery.md](grpc-service-discovery.md), [grpc-dial-context.md](grpc-dial-context.md).

---

## Observability

Wire traces and metrics as app components; use the structured logger with automatic trace/span ID injection.

```go
package main

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/logger"
	tracing "github.com/rushteam/beauty/pkg/service/telemetry"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, span := tracing.SpanFromContext(r.Context(), "handle-root")
		defer span.End()
		logger.Info("request", "path", r.URL.Path)
		_, _ = w.Write([]byte("ok"))
	})

	app := beauty.New(
		beauty.WithTrace(tracing.WithTraceStdoutExporter()), // or WithTraceOTLPGRPCExporter in prod
		beauty.WithMetric(),
		beauty.WithWebServer(":8080", mux),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

Logger usage anywhere in your code:

```go
logger.Info("server ready", "addr", ":8080")
logger.SetLevelByName("debug") // runtime, no restart
```

Mount `logger.LevelHandler()` on a debug route to adjust levels over HTTP. See [logger.md](logger.md). Prometheus example: [examples/complete/example](../examples/complete/example).

---

## Realtime: WebSocket

Attach WebSocket handlers to any `http.ServeMux` via `pkg/transport/ws`:

```go
package main

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/transport/ws"
)

func main() {
	mux := http.NewServeMux()
	mux.Handle("/echo", ws.Handler(func(r *http.Request, c *ws.Conn) error {
		ctx := r.Context()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return err
			}
			if err := c.Write(ctx, typ, data); err != nil {
				return err
			}
		}
	}))

	app := beauty.New(
		beauty.WithWebServer(":8080", mux, webserver.WithServiceName("ws-demo")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

Connect with any WebSocket client to `ws://localhost:8080/echo`. For JSON broadcast, combine `stream.New` with `ws.BroadcastJSON` — see [examples/websocket](../examples/websocket). SSE: [examples/sse](../examples/sse).

---

## What's Next

### Documentation

- [configuration.md](configuration.md) — all config backends
- [middleware.md](middleware.md) · [middleware-builtin.md](middleware-builtin.md) — middleware reference
- [grpc-service-discovery.md](grpc-service-discovery.md) · [grpc-dial-context.md](grpc-dial-context.md)
- [logger.md](logger.md) · [websocket.md](websocket.md) · [realtime-components.md](realtime-components.md)
- [wasm-roadmap.md](wasm-roadmap.md) · [k8s-rbac.md](k8s-rbac.md)

### Examples

- [examples/complete/example](../examples/complete/example) — HTTP + gRPC + gateway + OTel
- [examples/config](../examples/config) · [examples/security/auth-ratelimit](../examples/security/auth-ratelimit)
- [examples/grpc-service-discovery](../examples/grpc-service-discovery) · [examples/websocket](../examples/websocket)
- [examples/mq](../examples/mq) · [examples/cron-leader](../examples/cron-leader)
- [examples/](../examples/) — full catalog

### Optional integrations

Heavy deps are separate Go modules under [`contrib/`](../contrib/):

```bash
go get github.com/rushteam/beauty/contrib/gorm@latest   # GORM + OTel
go get github.com/rushteam/beauty/contrib/kafka@latest # Kafka for pkg/messaging/mq
go get github.com/rushteam/beauty/contrib/llm@latest   # LLM client
```

Full list: [contrib/README.md](../contrib/README.md).

### CLI

```bash
beauty new my-service    # scaffold
beauty api               # protobuf → gRPC + HTTP + OpenAPI
beauty dev               # hot-reload dev server
beauty build             # production build
```
