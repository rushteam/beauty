# WASM Roadmap

Plan for introducing WebAssembly into Beauty. Unified runtime choice: **wazero** (pure Go, zero CGo, embeddable),
following the "heavy dependencies in contrib, zero core burden" convention, landing in `contrib/wasm`. Same framework principle: **mechanisms, not policy**
— the framework provides "load + sandbox + ABI"; specific rules and logic live in the user's wasm modules; no filesystem/network by default, granted on demand.

## Tier 1 — WASM Plugin Runtime (Foundation) · Shipped (`contrib/wasm`)

`contrib/wasm` (based on wazero): write business logic and policies as sandboxed, hot-pluggable wasm modules, attached to Beauty extension points (`handler` / `middleware` / `governance` / `webhook`).

- Runtime wrapper: compile, instantiate, call exported functions, module caching;
- Controlled host functions (logging, KV, request metadata access, etc.), capabilities granted on demand;
- Memory limits (`WithMemoryLimitPages`) + execution timeout/interrupt (`WithTimeout` + `CloseOnContextDone`) + WASI filesystem and network disabled by default;
- High-level wrapper: **HTTP middleware as wasm modules** — request metadata → wasm `handle` → decision (allow/deny/rewrite headers/status code);
- `pkg/handler.WithMiddleware` generic hook, declarative wasm binding (zero contrib dependency in core).

Polish already added: instance pool (`WithPool`) + warm-up (`WithWarm` / `Pool.Warm`), disk compile cache (`WithCacheDir`),
built-in host functions (`WithLog` / `WithClock`), observability (`WithObserver` / `WithHandlerObserver`),
request body access (`WithBody`, opt-in length limit, downstream unaffected), Router metrics (`Stats`).
Remaining (non-essential): real guest examples (TinyGo / `//go:wasmexport` compile verification).

Use cases: custom middleware/filters, rate limiting/auth/rewrite policies, WAF rules, programmable webhooks.

## Tier 2 — WASM Sandbox for Agent Tools / Skills Scripts · Shipped (`contrib/wasmagent`)

Builds on existing `contrib/llm/agent` work: `skills.EnableExec` currently runs **local scripts** (disabled by default because it equals trusting arbitrary local commands).
Run inside a wazero WASI sandbox instead → from "trust local scripts" to "capability-limited, non-escapable" execution, closing the agent platform
security gap (E2B/code-interpreter approach, but pure Go embedded, no external process).

- `skills.ScriptExecutor` type + `WithScriptExecutor` injection point;
- `contrib/wasmagent.NewWasmExecutor`: adapts `skills.ScriptExecutor`, runs skill scripts in a wasm sandbox;
- `contrib/wasmagent.ToolFrom`: wraps a precompiled wasm module directly as an `agent.Tool`;
- LLM-generated code safe runtime (code-interpreter prototype; requires embedding an interpreter guest).

## Tier 3 — Policy as WASM · Shipped (`contrib/wasmopa`)

OPA compiles Rego to wasm; execute in a wazero sandbox, implementing `pkg/authz.Enforcer`:

    opa build -t wasm -e 'authz/allow' policy.rego → policy.wasm
    → wasmopa.New(wasmBytes) → authz.Enforcer

- OPA wasm ABI 1.2+ protocol (`opa_eval` one-shot evaluation);
- `Policy.Eval(ctx, input)`: generic policy evaluation for governance / any Rego decision;
- `Policy.Authorize(ctx, sub, action, resource)`: implements `authz.Enforcer`;
- Instance pool (each slot has independent Runtime + memory, concurrency-safe); `SetData` hot-updates external data;
- Pure Go (wazero), no CGo, no external process — much lighter than the full OPA SDK.

## Tier 4 — FaaS-lite: WASM Functions as HTTP Handlers · Shipped (`contrib/wasm`)

Beauty as a wasm function host: user uploads `.wasm` → register to a path → instance pool handles requests.
Shares alloc/handle ABI with Middleware; difference is guest outputs a **Response** (status + headers + body)
rather than a Decision (next/deny).

- `Handler(mod)`: wraps a single wasm module as `http.Handler`;
- `Router`: FaaS router — `Register` / `Deregister` / `RegisterBytes` hot-plug;
- Exact match > longest prefix match; concurrency-safe; supports instance pool (`WithHandlerPool`) + warm-up (`WithHandlerWarm`);
- Timeout (`WithHandlerTimeout`) + request body input (`WithHandlerBody`) + observability (`WithHandlerObserver`);
- `Router.Stats()`: Functions / Hits / Misses.

Usage:

```go
router := wasm.NewRouter(rt)
router.RegisterBytes(ctx, "/greet", greetWasm, wasm.WithHandlerPool(8))
http.Handle("/fn/", http.StripPrefix("/fn", router))
```

## Alternatives (Not Scheduled)

- Proxy-Wasm ABI compatibility: reuse existing Envoy Wasm filters (large engineering effort).
- js/wasm: compile shared logic from `gameloop` / `spatial` (AOI) / `presence` to the browser for client-side prediction.
- Deploy with GOOS=wasip1 to a wasm runtime: wasip1 networking is limited; currently suited only for pure compute handlers/workers.

**Not planned**: run the entire Beauty core as WASI (wasip1 lacks full support for networking, multiplexing, and signals).
