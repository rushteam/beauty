# higress-ai —— Higress AI 网关 × Beauty 集成示例

演示两个核心集成场景:

1. **LLM 请求经 Higress AI 网关代理** — 统一模型路由/限流/降级,服务不持有厂商 key
2. **MCP Server 经 Higress 暴露** — 外部 AI 客户端(Claude/IDE/Agent)安全调用本服务工具

```
┌──────────┐  HTTPS  ┌─────────────────┐  HTTP   ┌─────────────────────┐
│ AI Client├────────▶│ Higress Gateway ├────────▶│ beauty (本示例)      │
│ (Claude) │         │ JWT + 限流       │         │ /mcp (MCP Server)   │
└──────────┘         └────────┬────────┘         │ /chat (Agent)       │
                              │                   │ /stream (SSE)       │
                              │ AI Proxy 插件      └──────┬──────────────┘
                              ▼                          │ LLM 请求
                     ┌────────────────┐                  ▼
                     │ OpenAI / Qwen  │◀───── Higress /v1/chat/completions
                     └────────────────┘
```

> 独立嵌套模块(自带 `go.mod`,`replace` 指向本地核心与 contrib),
> 核心仓库 `go build ./...` 不会编译它。默认用**离线 stub 模型**,无需 API key 即可跑通。

## 运行

```bash
cd examples/higress-ai
go run .
```

### 使用 Higress AI 网关

```bash
# 设置 Higress 环境变量
export HIGRESS_AI_ENDPOINT=http://higress-gateway.higress-system.svc/v1
export HIGRESS_AI_TOKEN=my-consumer-token
go run .
```

### 使用直连 OpenAI(降级/开发)

```bash
export OPENAI_API_KEY=sk-xxx
export MODEL=gpt-4o-mini
go run .
```

### 环境变量

| 变量 | 说明 | 默认 |
|---|---|---|
| `HIGRESS_AI_ENDPOINT` | Higress AI 网关地址 | 无(不走网关) |
| `HIGRESS_AI_TOKEN` | 网关 consumer 凭证 | 无 |
| `OPENAI_API_KEY` | 直连 OpenAI key(降级用) | 无 |
| `OPENAI_BASE_URL` | 覆盖 OpenAI 地址 | `https://api.openai.com/v1` |
| `MODEL` | 模型名 | `gpt-4o-mini` |
| `ADDR` | 监听地址 | `:8080` |

优先级: Higress → 直连 OpenAI → 离线 stub。配了多个时用 `llm.Fallback` 自动降级。

## 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/` | 用法说明 |
| POST | `/chat` | body `{"message":"..."}` → `{"answer":"..."}` |
| GET | `/stream` | `?q=问题` → SSE: `tool` / `answer` 事件 |
| ANY | `/mcp` | MCP Streamable HTTP(外部 AI Client 调用) |
| GET | `/health` | 健康检查 |

## 试一试

```bash
# Agent 工具循环(stub 模式)
curl -s localhost:8080/chat -d '{"message":"现在几点"}'
# → {"answer":"(stub) 工具结果: 2026-08-19T03:00:00+08:00"}

curl -s localhost:8080/chat -d '{"message":"1加2"}'
# → {"answer":"(stub) 工具结果: 3.00"}

# SSE 流式
curl -N 'localhost:8080/stream?q=现在几点'

# MCP 工具列举(用 curl 模拟 MCP 客户端)
curl -s -X POST localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}'
```

## Higress 侧配置要点

### AI 网关(代理 LLM 请求)

Higress 的 `ai-proxy` 插件把 `/v1/chat/completions` 转发给模型厂商:

```yaml
apiVersion: extensions.higress.io/v1alpha1
kind: WasmPlugin
metadata:
  name: ai-proxy
spec:
  pluginConfig:
    provider:
      type: openai
      apiTokens: ["sk-xxx"]
      modelMapping:
        "*": "gpt-4o-mini"
```

### 暴露 MCP 端点

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcp-route
  annotations:
    higress.io/auth: |
      jwt:
        issuer: "https://auth.example.com"
        jwksUri: "https://auth.example.com/.well-known/jwks.json"
    higress.io/rate-limit: |
      rate_limit:
        requests_per_unit: 60
        unit: minute
    higress.io/cors: |
      cors:
        allow_origins: ["*"]
        allow_headers: ["Content-Type", "Authorization", "Mcp-Session-Id"]
spec:
  ingressClassName: higress
  rules:
    - host: mcp.example.com
      http:
        paths:
          - path: /mcp
            pathType: Prefix
            backend:
              service:
                name: higress-ai
                port:
                  number: 8080
```

## LLM 降级策略

```
┌────────────────┐     ┌──────────────────┐     ┌───────────┐
│ Higress AI GW  │────▶│  直连 OpenAI      │────▶│ 离线 Stub │
│ (主力)         │ 429 │  (降级)           │ err │ (兜底)    │
└────────────────┘     └──────────────────┘     └───────────┘
```

- Higress 正常 → 所有 LLM 请求走网关(统一管控)
- Higress 返回 429/5xx → `llm.Fallback` 自动切到直连
- 直连也失败 → 返回错误(生产不会走 stub)

## 涉及的包

- **LLM Client** — `contrib/llm/openai`(`WithBaseURL` 指向 Higress)
- **降级** — `contrib/llm`(`Fallback` / `FallbackConfig`)
- **计量** — `contrib/llm`(`Metered` 回调 token/延迟)
- **Agent** — `contrib/llm/agent`(`Runner` 工具循环)
- **MCP** — `contrib/mcp`(`HTTPHandler` Streamable HTTP)
- **SSE** — `pkg/transport/sse`(流式事件推送)
- **生命周期** — beauty 核心(`WithWebServer` 优雅启停)

## 相关文档

- [docs/higress-ai-gateway.md](../../docs/higress-ai-gateway.md) — 完整集成指南
- [docs/higress-gateway.md](../../docs/higress-gateway.md) — 基础网关(服务发现/路由)
- [examples/agentservice](../agentservice/) — Agent 全链路(无 Higress)
- [contrib/mcp](../../contrib/mcp/) — MCP 集成说明
