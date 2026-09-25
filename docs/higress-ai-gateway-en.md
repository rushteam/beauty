# Higress AI Gateway × Beauty LLM / MCP Integration

> 中文版: [higress-ai-gateway.md](higress-ai-gateway.md)

This document describes how to combine the **AI gateway** capabilities of [Higress](https://higress.io) with beauty's
`contrib/llm` (LLM client + Agent) and `contrib/mcp` (MCP Server/Client).

> For basic service discovery and routing, see [docs/higress-gateway-en.md](higress-gateway-en.md).
> This document focuses on two AI-related scenarios:
> 1. **LLM requests** issued by Beauty are uniformly proxied through the Higress AI gateway
> 2. **MCP endpoints** exposed by Beauty are made securely accessible to the outside world via Higress

---

## 1. Routing LLM Requests Through the Higress AI Gateway

### How It Works

The Higress AI gateway exposes an OpenAI-compatible `/v1/chat/completions` endpoint and internally handles model routing,
token rate limiting, fallback, and observability. beauty's `contrib/llm/openai` natively supports `WithBaseURL`,
so all you need to do is point the BaseURL at Higress — **zero code changes, pure configuration**.

### Architecture

```
┌─────────────┐       ┌───────────────────────┐       ┌──────────────┐
│ Beauty App  │──────▶│  Higress AI Gateway   │──────▶│ OpenAI / DS  │
│ contrib/llm │ HTTP  │                       │       │ / Qwen / ... │
│  openai.New │       │ • Model routing (hdr) │       └──────────────┘
│  BaseURL ──────────▶│ • Token limit/billing │
│             │       │ • Fallback            │
│             │       │ • Req/resp logging    │
└─────────────┘       └───────────────────────┘
```

### Basic Usage

```go
import "github.com/rushteam/beauty/contrib/llm/openai"

// Higress AI gateway address
// Inside K8s: http://higress-gateway.higress-system.svc/v1
// External:   https://ai-gw.example.com/v1
higressAI := os.Getenv("HIGRESS_AI_ENDPOINT")

client := openai.New(
    os.Getenv("HIGRESS_AI_TOKEN"), // gateway-level consumer credential
    openai.WithBaseURL(higressAI),
)
```

### Fallback: Connect Directly When the Gateway Fails

Combined with `llm.Fallback` / `llm.FallbackConfig`, you can fall back to connecting directly to the vendor when Higress is unavailable:

```go
import "github.com/rushteam/beauty/contrib/llm"

directClient := openai.New(os.Getenv("OPENAI_API_KEY"))

// simple fallback
client := llm.Fallback(higressClient, directClient)

// tiered by error type
client := llm.FallbackConfig{
    Primary:     higressClient,
    OnRateLimit: []llm.Client{directClient}, // bypass when the gateway returns 429
    OnError:     []llm.Client{directClient}, // connect directly when the gateway fails
    OnFallback: func(ctx context.Context, kind llm.ErrorKind, _, _ string, err error) {
        slog.WarnContext(ctx, "LLM fallback", "kind", kind, "err", err)
    },
}.Build()
```

### Attaching Routing Metadata

Use a custom Transport to attach headers to LLM requests so that Higress can perform smart routing:

```go
type higressTransport struct {
    base    http.RoundTripper
    service string
}

func (t *higressTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    req.Header.Set("X-Beauty-Service", t.service) // Higress routes by service
    req.Header.Set("X-Model-Tier", "fast")        // let Higress pick a fast model
    return t.base.RoundTrip(req)
}

hc := &http.Client{Transport: &higressTransport{
    base: http.DefaultTransport, service: "agentservice",
}}
client := openai.New(token, openai.WithBaseURL(higressAI), openai.WithHTTPClient(hc))
```

### Higress-Side Configuration Reference

```yaml
# AI Route: model routing + rate limiting
apiVersion: extensions.higress.io/v1alpha1
kind: WasmPlugin
metadata:
  name: ai-proxy
  namespace: higress-system
spec:
  pluginConfig:
    provider:
      type: openai
      apiTokens:
        - "sk-xxx"
      modelMapping:
        "gpt-4o-mini": "gpt-4o-mini"
        "gpt-4o": "gpt-4o"
        "*": "gpt-4o-mini"  # route to the cheap model by default
    # Token rate limiting
    rateLimiting:
      tokensPerMinute: 100000
      requestsPerMinute: 200
---
# Or weighted routing across multiple providers
apiVersion: extensions.higress.io/v1alpha1
kind: WasmPlugin
metadata:
  name: ai-fallback
spec:
  pluginConfig:
    providers:
      - type: openai
        weight: 80
      - type: dashscope  # Tongyi Qianwen (Qwen) as backup
        weight: 20
        apiTokens: ["sk-ali-xxx"]
```

### Metering: Dual Accounting at the Gateway and Application Layers

```go
// Application layer: llm.Metered records tokens and latency for each request
client = llm.Metered(client, func(ctx context.Context, model string, u llm.Usage, d time.Duration) {
    // push to OTel / logs / billing
    slog.Info("llm usage", "model", model,
        "input", u.InputTokens, "output", u.OutputTokens, "latency", d)
})

// Application-layer budget cap (e.g. 100k tokens per user)
budgeted := llm.Budget(client, 100_000)
```

At the gateway layer, Higress ships with built-in AI observability (requests / tokens / latency / error-rate dashboards); the two layers complement each other.

---

## 2. Exposing MCP Endpoints Through Higress

### How It Works

`contrib/mcp`'s `HTTPHandler(server)` returns a standard `http.Handler` (Streamable HTTP protocol),
which is mounted on the beauty webserver. Higress, as the ingress gateway, handles TLS termination, authentication, rate limiting, and CORS;
beauty only deals with tool logic.

### Architecture

```
┌──────────────┐       ┌──────────────────────┐       ┌─────────────────┐
│  AI Client   │       │   Higress Gateway    │       │  Beauty Service │
│ (Claude/IDE/ │──────▶│                      │──────▶│                 │
│  Agent)      │ HTTPS │ • JWT / OAuth        │  HTTP │ /mcp endpoint   │
│              │       │ • Rate Limit         │       │ mcp.HTTPHandler │
│              │       │ • CORS (AI clients)  │       │                 │
└──────────────┘       └──────────────────────┘       └─────────────────┘
```

### Beauty-Side Implementation

```go
import (
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/contrib/mcp"
    "github.com/rushteam/beauty/pkg/service/webserver"
)

srv := mcp.NewServer("my-tools", "1.0.0")

// register a tool (type-safe; the SDK reflects the JSON Schema automatically)
type QueryIn struct {
    SQL string `json:"sql" jsonschema:"description=read-only SQL query"`
}
type QueryOut struct {
    Rows int `json:"rows"`
}
mcp.AddTool(srv, &mcp.Tool{Name: "query", Description: "execute a read-only query"},
    func(ctx context.Context, _ *mcp.CallToolRequest, in QueryIn) (*mcp.CallToolResult, QueryOut, error) {
        rows := doQuery(ctx, in.SQL)
        return mcp.Text(fmt.Sprintf("%d rows", rows)), QueryOut{Rows: rows}, nil
    })

mux := http.NewServeMux()
mux.Handle("/mcp", mcp.HTTPHandler(srv))
mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

app := beauty.New(
    beauty.WithWebServer(":8080", mux, webserver.WithServiceName("mcp-tools")),
)
app.Start(context.Background())
```

### Higress Route Configuration

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcp-tools
  annotations:
    # Authentication: JWT validation
    higress.io/auth: |
      jwt:
        issuer: "https://auth.example.com"
        jwksUri: "https://auth.example.com/.well-known/jwks.json"
    # Rate limiting: 60 req/min per consumer
    higress.io/rate-limit: |
      rate_limit:
        requests_per_unit: 60
        unit: minute
        limit_by_header: "Authorization"
    # CORS: cross-origin access for MCP clients
    higress.io/cors: |
      cors:
        allow_origins: ["*"]
        allow_methods: ["GET", "POST", "DELETE", "OPTIONS"]
        allow_headers: ["Content-Type", "Authorization", "Mcp-Session-Id"]
        expose_headers: ["Mcp-Session-Id"]
spec:
  ingressClassName: higress
  tls:
    - hosts: ["mcp.example.com"]
      secretName: mcp-tls
  rules:
    - host: mcp.example.com
      http:
        paths:
          - path: /mcp
            pathType: Prefix
            backend:
              service:
                name: mcp-tools
                port:
                  number: 8080
```

### MCP Client Connection (via Higress)

```go
// other beauty services / any Go program acting as an MCP client
sess, err := mcp.DialHTTP(ctx, "my-client", "https://mcp.example.com/mcp")
if err != nil { ... }
defer sess.Close()

// list tools
tools, _ := sess.ListTools(ctx, nil)

// call a tool
result, _ := sess.CallTool(ctx, &sdk.CallToolParams{
    Name: "query",
    Arguments: map[string]any{"sql": "SELECT count(*) FROM users"},
})
```

---

## 3. Full Architecture: Both LLM and MCP Through Higress

```
                    ┌──────────────────────────────────────────────────┐
                    │                 Kubernetes Cluster                │
                    │                                                  │
┌──────────┐ HTTPS │  ┌────────────────────────┐                      │
│ AI Client├───────┼─▶│    Higress Gateway     │                      │
│ (Claude) │       │  │                        │                      │
└──────────┘       │  │  /mcp ──────────────── ┼─▶ beauty:8080/mcp   │
                    │  │                        │                      │
┌──────────┐ HTTPS │  │  /api/chat ─────────── ┼─▶ beauty:8080/chat  │
│  User    ├───────┼─▶│                        │                      │
└──────────┘       │  │  /v1/chat/completions  │                      │
                    │  │  (AI Proxy plugin) ─── ┼─▶ OpenAI/DS/Qwen   │
                    │  └────────────────────────┘                      │
                    │             ▲                                     │
                    │             │ (beauty LLM requests)                │
                    │  ┌──────────┴────────┐                           │
                    │  │  Beauty Service    │                           │
                    │  │  • LLM Client ────┼── BaseURL → Higress /v1  │
                    │  │  • MCP Server      │                           │
                    │  │  • Agent Runner    │                           │
                    │  └───────────────────┘                           │
                    └──────────────────────────────────────────────────┘
```

### Division of Traffic Responsibilities

| Traffic direction | Path | Higress responsibility | Beauty responsibility |
|---|---|---|---|
| External→MCP | `mcp.example.com/mcp` | TLS + JWT + rate limiting + CORS | Tool logic |
| External→Business | `api.example.com/chat` | TLS + authentication + WAF | Agent orchestration |
| Beauty→LLM | Internal → Higress `/v1/*` | Model routing + token rate limiting + fallback | Request construction + response parsing |

---

## 4. Best Practices

### Environment Variable Conventions

```bash
# Recommended environment variables for Beauty services
HIGRESS_AI_ENDPOINT=http://higress-gateway.higress-system.svc/v1  # inside K8s
HIGRESS_AI_TOKEN=consumer-token-xxx                                # gateway consumer credential
MODEL=gpt-4o-mini                                                  # default model

# Direct-connection fallback (optional)
OPENAI_API_KEY=sk-xxx          # key for connecting directly to OpenAI (used when Higress fails)
```

### Security Layers

```
Layer 1 — Higress:  TLS termination / JWT validation / IP ACL / HTTP-flood (CC) protection / global rate limiting
Layer 2 — Beauty:   business authorization (pkg/middleware/auth) / RBAC (pkg/api/authz)
Layer 3 — LLM:     Guard guardrails (prompt injection / PII / max input)
Layer 4 — MCP:     tool-level permission checks (validated inside each handler)
```

### SSE / Long-Lived Connection Pass-Through

Higress is built on Envoy and natively supports HTTP/2 + SSE pass-through. The following scenarios need no extra configuration:
- The streaming `Stream()` method of `contrib/llm/openai` (SSE)
- `contrib/mcp`'s Streamable HTTP (long-lived connection + message push)
- The agent event stream of `pkg/transport/sse`

To adjust timeouts (default Envoy idle timeout is 5m):

```yaml
metadata:
  annotations:
    higress.io/proxy-read-timeout: "300"   # seconds, suited to long LLM generations
    higress.io/proxy-send-timeout: "300"
```

---

## 5. Comparison with Direct Connection

| | Direct to model vendor | Via Higress AI gateway |
|---|---|---|
| Configuration | Each service configures key + endpoint | Keys managed centrally by the gateway; services only hold a consumer token |
| Model switching | Change code/config and restart | Hot-update routing rules on the Higress side, no service restart |
| Rate limiting | Each service implements its own | Unified token/request-level rate limiting at the gateway |
| Observability | Each service instruments itself | Gateway dashboard + application-layer Metered, complementary |
| Fallback | Must be configured manually with `llm.Fallback` | Native gateway fallback + application-layer second safety net |
| Secret management | Scattered across each service's env | Centrally managed; services never touch vendor keys |
| Latency | Lowest with direct connection | +1 hop (<1ms within the same cluster) |

**Recommendation**: use Higress in production (unified governance) and connect directly in development/testing (simple and fast).
Chain the two with `llm.Fallback` for seamless switching.

---

## 6. Related Documents

- [Higress basic gateway integration](higress-gateway-en.md) — service discovery, routing, gRPC transcoding
- [Higress AI gateway official docs](https://higress.io/docs/latest/plugins/ai/)
- [contrib/llm README](../contrib/llm/) — LLM client API
- [contrib/mcp README](../contrib/mcp/) — MCP integration
- [examples/higress-ai](../examples/higress-ai/) — complete runnable example
