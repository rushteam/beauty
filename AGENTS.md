# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## What this is

Beauty is a Go 1.26 microservice framework. A tiny core (`beauty.New(...).Start(ctx)`) runs any number of services under one graceful lifecycle. Everything is "mechanisms, not policy": each `pkg/*` package solves one problem and stays out of business logic. Heavy/optional integrations (GORM, Kafka, LLM, WASM…) live under `contrib/` as separate modules you pull only when used.

## Module boundaries (important)

The repo is **three independent Go modules** — respect the boundary or CI fails:

- **Core** — `github.com/rushteam/beauty` (root `go.mod`). Light, stdlib-leaning. **Must never import `contrib/`.** CI enforces this: `go list -deps ./...` must not contain any `contrib/` path.
- **`contrib/<name>`** — each subdir is its own module (`github.com/rushteam/beauty/contrib/<name>`) with its own `go.mod`, for heavy third-party deps (gorm, kafka, elasticsearch, llm, wasm…). Build/test *inside* the subdir. `contrib` code is written against core interfaces (e.g. `pkg/mq.Publisher`, slog, OTel global providers) so users can swap in their own implementation.
- **`tools/`** — `github.com/rushteam/beauty/tools`, the `beauty` CLI (scaffolding, api codegen, dev hot-reload). Separate module.

Core `go build ./...` / `go test ./...` does **not** compile `contrib/` — the module boundary isolates it.

## Commands

```bash
# Core (run from repo root) — mirror of CI
gofmt -l .                        # must print nothing; CI fails on unformatted files
go vet ./...
go build ./...
go test -race -timeout 600s ./...
go test -race ./pkg/dlock/...     # single package
go test -race -run TestName ./pkg/dlock/...   # single test

# contrib module — always cd into it first (separate go.mod)
cd contrib/gorm && go test ./...

# tools / CLI
cd tools && go build -o beauty ./cmd/beauty
go install github.com/rushteam/beauty/tools/cmd/beauty@latest
```

CI (`.github/workflows/ci.yml`) runs, per module: gofmt check, `go vet`, `go build`, `go test -race`, the "no contrib pollution" check, and `govulncheck ./...`. Run at least `gofmt -l .` and `go test -race ./...` before considering core work done.

The `beauty` CLI subcommands: `new` (scaffold project/service), `add`, `api` (parse .proto / api.spec → gRPC+HTTP+OpenAPI codegen), `dev` (hot-reload run), `build`.

## Core architecture

The whole framework is `beauty.go` + a small set of interfaces. Read `beauty.go` first.

- **`Service`** (`beauty.go`) — `Start(ctx) error` + `String() string`. Anything satisfying this composes into the app. Built-in services live in `pkg/service/{webserver,grpcserver,cron,pprof}`; helper options are in `service.go` (`WithWebServer`, `WithGrpcServer`, `WithCrontab`, `WithPprof`).
- **`Option`** — functional options passed to `beauty.New(...)`. This is the only assembly mechanism; there is no config struct.
- **`Component`** (`pkg/service/core`) — `Name()` + `Init() context.CancelFunc`, a cross-cutting concern with lifecycle (config reload, telemetry). Added via `WithComponent`; `WithConfig`/`WithTrace`/`WithMetric` wrap it.
- **Graceful shutdown ordering** is the subtle part of `App.startService`. Each service gets **two goroutines**: an *orchestration* goroutine and a *serve* goroutine. On shutdown the order is strictly **deregister from registry → wait `drainDelay` (let clients/LB notice) → stop the server**. `serveCtx` uses `context.WithoutCancel` so it keeps parent ctx *values* (logger/trace) but is stopped explicitly by the orchestrator, not by parent cancellation. Any service's `Start` returning triggers full-app shutdown. Preserve this ordering when touching lifecycle code.
- **`ReadyNotifier`** (optional `Ready() <-chan struct{}`) — a service signals it is accepting traffic; registration waits for it before advertising to the registry.

## Service discovery

`pkg/service/discover` defines `Registry` / `Discovery` / `RegistryDiscovery` interfaces with backends: `etcdv3`, `consul`, `nacos`, `polaris`, `k8s` (+ `noop`). Providers register via `WithRegistry`; callers dial by name. Client side is `pkg/client/grpcclient` (e.g. `DialContext(ctx, "beauty://svc.name?label=v", ...)` with load balancers like `p2c_ewma`) and `pkg/client/http`.

## Layout map

- `pkg/service/*` — the runnable services + discovery/logger/telemetry.
- `pkg/middleware/*` — composable HTTP middleware (accesslog, auth, ratelimit, circuitbreaker, timeout, cache, cors, recovery, requestid…). Default chain order is documented in `docs/middleware*.md`.
- `pkg/*` (70+) — single-purpose mechanisms: `cache`, `dlock`, `idgen`, `kvstore`, `mq`, `eventbus`, `saga`, `txn`, `ws`, `sse`, `quic`, `hls`, `media`, `gameloop`, `spatial`, `presence`, `ratelimit`, `hedge`, `backoff`, `circuitbreaker`, `shard`, etc.
- `examples/` — many runnable single-purpose demos; the fastest way to see an API in use.
- `docs/` — per-topic design notes (discovery, middleware, media, wasm-roadmap, k8s-rbac…).
- `contrib/README.md` — table of every contrib module and its purpose.

## Conventions

- Comments and CLI text are largely in Chinese; match the surrounding language of the file you edit.
- Prefer stdlib in core; if a change would add a heavy dependency to the root `go.mod`, it probably belongs in a `contrib/` module instead.
- Concurrency correctness matters here (framework code, `-race` in CI). Match the existing use of `sync/atomic`, `sync.WaitGroup`, and careful context handling rather than introducing new patterns.
