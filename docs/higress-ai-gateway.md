# Higress AI 网关 × Beauty LLM / MCP 集成

本文档介绍如何将 [Higress](https://higress.io) 的 **AI 网关**能力与 beauty 的
`contrib/llm`（LLM 客户端 + Agent）和 `contrib/mcp`（MCP Server/Client）结合使用。

> 基础服务发现与路由见 [docs/higress-gateway.md](higress-gateway.md)。
> 本文聚焦 AI 相关的两个场景：
> 1. Beauty 发出的 **LLM 请求**统一经 Higress AI 网关代理
> 2. Beauty 暴露的 **MCP 端点**经 Higress 对外提供安全访问

---

## 一、LLM 请求走 Higress AI 网关

### 原理

Higress AI 网关对外暴露 OpenAI 兼容的 `/v1/chat/completions` 端点，内部做模型路由、
token 限流、fallback、可观测。beauty 的 `contrib/llm/openai` 天然支持 `WithBaseURL`，
只需把 BaseURL 指向 Higress 即可——**零代码改动，纯配置**。

### 架构

```
┌─────────────┐       ┌───────────────────────┐       ┌──────────────┐
│ Beauty App  │──────▶│  Higress AI Gateway   │──────▶│ OpenAI / DS  │
│ contrib/llm │ HTTP  │                       │       │ / Qwen / ... │
│  openai.New │       │ • 模型路由(按header) │       └──────────────┘
│  BaseURL ──────────▶│ • Token 限流/计费      │
│             │       │ • Fallback 降级       │
│             │       │ • 请求/响应日志       │
└─────────────┘       └───────────────────────┘
```

### 基本用法

```go
import "github.com/rushteam/beauty/contrib/llm/openai"

// Higress AI 网关地址
// K8s 内部: http://higress-gateway.higress-system.svc/v1
// 外部:     https://ai-gw.example.com/v1
higressAI := os.Getenv("HIGRESS_AI_ENDPOINT")

client := openai.New(
    os.Getenv("HIGRESS_AI_TOKEN"), // 网关层 consumer credential
    openai.WithBaseURL(higressAI),
)
```

### Fallback：网关故障自动直连

配合 `llm.Fallback` / `llm.FallbackConfig`，当 Higress 不可用时降级到直连厂商：

```go
import "github.com/rushteam/beauty/contrib/llm"

directClient := openai.New(os.Getenv("OPENAI_API_KEY"))

// 简单降级
client := llm.Fallback(higressClient, directClient)

// 按错误类型分级
client := llm.FallbackConfig{
    Primary:     higressClient,
    OnRateLimit: []llm.Client{directClient}, // 网关 429 时绕过
    OnError:     []llm.Client{directClient}, // 网关故障时直连
    OnFallback: func(ctx context.Context, kind llm.ErrorKind, _, _ string, err error) {
        slog.WarnContext(ctx, "LLM fallback", "kind", kind, "err", err)
    },
}.Build()
```

### 附加路由元信息

通过自定义 Transport 为 LLM 请求附加 header，让 Higress 做智能路由：

```go
type higressTransport struct {
    base    http.RoundTripper
    service string
}

func (t *higressTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    req.Header.Set("X-Beauty-Service", t.service) // Higress 按服务路由
    req.Header.Set("X-Model-Tier", "fast")        // 让 Higress 选快速模型
    return t.base.RoundTrip(req)
}

hc := &http.Client{Transport: &higressTransport{
    base: http.DefaultTransport, service: "agentservice",
}}
client := openai.New(token, openai.WithBaseURL(higressAI), openai.WithHTTPClient(hc))
```

### Higress 侧配置参考

```yaml
# AI Route: 模型路由 + 限流
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
        "*": "gpt-4o-mini"  # 默认路由到便宜模型
    # Token 限流
    rateLimiting:
      tokensPerMinute: 100000
      requestsPerMinute: 200
---
# 或多 provider 加权路由
apiVersion: extensions.higress.io/v1alpha1
kind: WasmPlugin
metadata:
  name: ai-fallback
spec:
  pluginConfig:
    providers:
      - type: openai
        weight: 80
      - type: dashscope  # 通义千问备用
        weight: 20
        apiTokens: ["sk-ali-xxx"]
```

### 计量：网关层 + 应用层双重统计

```go
// 应用层: llm.Metered 记录每次请求的 token 和延迟
client = llm.Metered(client, func(ctx context.Context, model string, u llm.Usage, d time.Duration) {
    // 推到 OTel / 日志 / 账单
    slog.Info("llm usage", "model", model,
        "input", u.InputTokens, "output", u.OutputTokens, "latency", d)
})

// 应用层预算上限(如每用户 10 万 token)
budgeted := llm.Budget(client, 100_000)
```

网关层 Higress 自带 AI 可观测（请求/token/延迟/错误率 dashboard）,两层互补。

---

## 二、MCP 端点走 Higress 暴露

### 原理

`contrib/mcp` 的 `HTTPHandler(server)` 返回标准 `http.Handler`（Streamable HTTP 协议）,
挂到 beauty webserver 上。Higress 作为入口网关负责 TLS 终止、认证、限流、CORS；
beauty 只管工具逻辑。

### 架构

```
┌──────────────┐       ┌──────────────────────┐       ┌─────────────────┐
│  AI Client   │       │   Higress Gateway    │       │  Beauty Service │
│ (Claude/IDE/ │──────▶│                      │──────▶│                 │
│  Agent)      │ HTTPS │ • JWT / OAuth        │  HTTP │ /mcp endpoint   │
│              │       │ • Rate Limit         │       │ mcp.HTTPHandler │
│              │       │ • CORS (AI clients)  │       │                 │
└──────────────┘       └──────────────────────┘       └─────────────────┘
```

### Beauty 侧实现

```go
import (
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/contrib/mcp"
    "github.com/rushteam/beauty/pkg/service/webserver"
)

srv := mcp.NewServer("my-tools", "1.0.0")

// 注册工具(类型安全,SDK 自动反射 JSON Schema)
type QueryIn struct {
    SQL string `json:"sql" jsonschema:"description=只读 SQL 查询"`
}
type QueryOut struct {
    Rows int `json:"rows"`
}
mcp.AddTool(srv, &mcp.Tool{Name: "query", Description: "执行只读查询"},
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

### Higress 路由配置

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcp-tools
  annotations:
    # 认证: JWT 校验
    higress.io/auth: |
      jwt:
        issuer: "https://auth.example.com"
        jwksUri: "https://auth.example.com/.well-known/jwks.json"
    # 限流: 每 consumer 60 req/min
    higress.io/rate-limit: |
      rate_limit:
        requests_per_unit: 60
        unit: minute
        limit_by_header: "Authorization"
    # CORS: MCP 客户端跨域
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

### MCP 客户端连接（经 Higress）

```go
// 其他 beauty 服务 / 任意 Go 程序作为 MCP 客户端
sess, err := mcp.DialHTTP(ctx, "my-client", "https://mcp.example.com/mcp")
if err != nil { ... }
defer sess.Close()

// 列举工具
tools, _ := sess.ListTools(ctx, nil)

// 调用工具
result, _ := sess.CallTool(ctx, &sdk.CallToolParams{
    Name: "query",
    Arguments: map[string]any{"sql": "SELECT count(*) FROM users"},
})
```

---

## 三、完整架构：LLM + MCP 全走 Higress

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
│  用户    ├───────┼─▶│                        │                      │
└──────────┘       │  │  /v1/chat/completions  │                      │
                    │  │  (AI Proxy 插件) ───── ┼─▶ OpenAI/DS/Qwen   │
                    │  └────────────────────────┘                      │
                    │             ▲                                     │
                    │             │ (beauty LLM 请求)                    │
                    │  ┌──────────┴────────┐                           │
                    │  │  Beauty Service    │                           │
                    │  │  • LLM Client ────┼── BaseURL → Higress /v1  │
                    │  │  • MCP Server      │                           │
                    │  │  • Agent Runner    │                           │
                    │  └───────────────────┘                           │
                    └──────────────────────────────────────────────────┘
```

### 流量分工

| 流量方向 | 路径 | Higress 职责 | Beauty 职责 |
|---|---|---|---|
| 外部→MCP | `mcp.example.com/mcp` | TLS + JWT + 限流 + CORS | 工具逻辑 |
| 外部→业务 | `api.example.com/chat` | TLS + 认证 + WAF | Agent 编排 |
| Beauty→LLM | 内部 → Higress `/v1/*` | 模型路由 + token 限流 + fallback | 请求构造 + 结果解析 |

---

## 四、最佳实践

### 环境变量约定

```bash
# Beauty 服务推荐的环境变量
HIGRESS_AI_ENDPOINT=http://higress-gateway.higress-system.svc/v1  # K8s 内部
HIGRESS_AI_TOKEN=consumer-token-xxx                                # 网关 consumer 凭证
MODEL=gpt-4o-mini                                                  # 默认模型

# 直连降级(可选)
OPENAI_API_KEY=sk-xxx          # 直连 OpenAI 的 key(Higress 故障时用)
```

### 安全层次

```
Layer 1 — Higress:  TLS 终止 / JWT 校验 / IP ACL / CC 防护 / 全局限流
Layer 2 — Beauty:   业务鉴权(pkg/middleware/auth) / RBAC(pkg/authz)
Layer 3 — LLM:     Guard 护栏(prompt injection / PII / max input)
Layer 4 — MCP:     工具级权限检查(handler 内自行校验)
```

### SSE / 长连接透传

Higress 基于 Envoy，原生支持 HTTP/2 + SSE 透传。以下场景无需额外配置：
- `contrib/llm/openai` 的流式 `Stream()` 方法（SSE）
- `contrib/mcp` 的 Streamable HTTP（长连接 + 消息推送）
- `pkg/sse` 的 agent 事件流

如需调整超时（默认 Envoy idle timeout 5m）：

```yaml
metadata:
  annotations:
    higress.io/proxy-read-timeout: "300"   # 秒，适配长 LLM 生成
    higress.io/proxy-send-timeout: "300"
```

---

## 五、与直连模式的对比

| | 直连模型厂商 | 经 Higress AI 网关 |
|---|---|---|
| 配置 | 每服务配 key + endpoint | 统一网关管理 key，服务只拿 consumer token |
| 模型切换 | 改代码/配置重启 | Higress 侧热更路由规则，无需重启服务 |
| 限流 | 各服务自己实现 | 网关统一 token/请求级限流 |
| 可观测 | 各服务各自埋点 | 网关 dashboard + 应用层 Metered 互补 |
| 降级 | 需 `llm.Fallback` 手动配 | 网关原生 fallback + 应用层二次兜底 |
| 密钥管理 | 散落各服务 env | 集中管理，服务不接触厂商 key |
| 延迟 | 直连最低 | +1 hop（同集群 <1ms） |

**建议**：生产环境走 Higress（统一治理），开发/测试直连（简单快速）。
`llm.Fallback` 串联两者实现无缝切换。

---

## 六、相关文档

- [Higress 基础网关集成](higress-gateway.md) — 服务发现、路由、gRPC transcoding
- [Higress AI 网关官方文档](https://higress.io/docs/latest/plugins/ai/)
- [contrib/llm README](../contrib/llm/) — LLM 客户端 API
- [contrib/mcp README](../contrib/mcp/) — MCP 集成
- [examples/higress-ai](../examples/higress-ai/) — 可运行的完整示例
