# contrib/otelllm — AI 可观测性(LangSmith/Langfuse 兼容)

为 `contrib/llm` 提供 **可插拔、非侵入** 的 OpenTelemetry 可观测性支持。

## 能力一览

| 能力 | 说明 |
|---|---|
| **Trace 装饰器** | 每次 `Generate`/`Stream` 自动创建 OTel Span(遵循 [GenAI 语义约定](https://opentelemetry.io/docs/specs/semconv/gen-ai/)) |
| **Metrics** | 调用计数、token 用量(input/output)、延迟直方图、错误计数 |
| **Agent Hooks** | Agent 循环每步(模型调用/工具执行)自动创建 parent-child span,形成 run tree |
| **增强 Metered** | 同时上报成功和失败调用,弥补原版 `llm.Metered` 只报成功的局限 |

## 设计原则

- **零侵入**：通过装饰 `llm.Client` 和注入 `agent.Hooks` 实现,不修改 `contrib/llm` 核心
- **可插拔**：不用就不引——`contrib/llm` 本身保持零依赖
- **标准协议**：输出的是标准 OTel Span/Metrics,可导出到任何 OTel 兼容后端(Jaeger、Grafana Tempo、Datadog、Langfuse、LangSmith 等)

## 快速开始

### 1. 包装 Client(自动 Trace + Metrics)

```go
import (
    "github.com/rushteam/beauty/contrib/llm/openai"
    "github.com/rushteam/beauty/contrib/otelllm"
)

// 一行包装,自动为每次 LLM 调用创建 Span 和 Metrics
client := otelllm.Instrument(
    openai.New(apiKey),
    otelllm.WithSystem("openai"),     // 标注 AI provider
    otelllm.WithRecordPrompt(),       // 可选:记录 prompt 到 span events(注意敏感数据)
)

resp, err := client.Generate(ctx, llm.Request{
    Model:    "gpt-4",
    Messages: []llm.Message{{Role: llm.User, Content: "你好"}},
})
```

生成的 Span 属性(遵循 OTel GenAI Semantic Conventions):

```
span name:  "chat gpt-4"
attributes:
  gen_ai.system:              "openai"
  gen_ai.operation.name:      "chat"
  gen_ai.request.model:       "gpt-4"
  gen_ai.response.model:      "gpt-4"
  gen_ai.usage.input_tokens:  500
  gen_ai.usage.output_tokens: 120
  gen_ai.request.max_tokens:  4096
  gen_ai.request.temperature: 0.7
```

### 2. Agent 级别 Span(run tree)

```go
import "github.com/rushteam/beauty/contrib/otelllm"

runner := &agent.Runner{
    Client:   otelllm.Instrument(openai.New(key), otelllm.WithSystem("openai")),
    Tools:    myTools,
    Hooks:    otelllm.NewAgentHooks(),
    MaxSteps: 10,
}
```

生成的 Span 树(在 Jaeger/Tempo/Langfuse 中可视化):

```
[agent.run]
  ├─ [agent.model gpt-4 step=1]  input_tokens=500 output_tokens=80
  ├─ [agent.tool query_order step=1]  duration=120ms
  ├─ [agent.model gpt-4 step=2]  input_tokens=650 output_tokens=120
  └─ [agent.tool send_email step=2]  duration=300ms
```

### 3. 增强版 Metered(含错误上报)

```go
// 原版 llm.Metered 只在成功时回调,MeteredWithErrors 同时上报失败
client := otelllm.MeteredWithErrors(
    openai.New(key),
    otelllm.OTelUsageHook(),  // 开箱即用的 OTel Metrics 回调
    "openai",
)
```

### 4. 自由组合

```go
// Trace + Metrics + Retry + Fallback:全部通过装饰器组合
client := otelllm.Instrument(
    llm.Retry(
        llm.Fallback(
            openai.New(primaryKey),
            anthropic.New(backupKey),
        ),
        3, time.Second,
    ),
    otelllm.WithSystem("openai"),
)
```

## 接入 Langfuse / LangSmith

由于输出是标准 OTel 协议,只需配置 OTLP Exporter 到对应平台:

```go
// Langfuse: 配置 OTLP Exporter 指向 Langfuse 的 OTel 端点
// export OTEL_EXPORTER_OTLP_ENDPOINT=https://cloud.langfuse.com/api/public/otel
// export OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic <base64(publicKey:secretKey)>

// LangSmith: 使用 LangSmith 的 OTel 采集器
// export OTEL_EXPORTER_OTLP_ENDPOINT=https://api.smith.langchain.com/otel
// export OTEL_EXPORTER_OTLP_HEADERS=x-api-key=<your-langsmith-api-key>
```

## API 参考

### Instrument(c llm.Client, opts ...Option) llm.Client

装饰 Client,自动 Trace + Metrics。

Options:
- `WithSystem(name)` — AI provider 名(如 "openai", "anthropic")
- `WithTracerProvider(tp)` — 自定义 TracerProvider(默认全局)
- `WithMeterProvider(mp)` — 自定义 MeterProvider(默认全局)
- `WithRecordPrompt()` — 在 span events 中记录 prompt 内容

### NewAgentHooks(opts ...AgentHookOption) agent.Hooks

创建 Agent Hooks,为每步自动创建 OTel Span。

### MergeHooks(a, b agent.Hooks) agent.Hooks

合并两个 Hooks,依次调用(用于同时使用 OTel hooks 和自定义 hooks)。

### MeteredWithErrors(c llm.Client, hook UsageReportHook, system string) llm.Client

增强版 Metered,成功/失败均回调。

### OTelUsageHook(opts ...MetricHookOption) UsageReportHook

返回开箱即用的 OTel Metrics 回调,配合 MeteredWithErrors 使用。

### RecordRunOutcome(span trace.Span, outcome agent.RunOutcome)

在 span 上记录 Agent 运行结果(token 总量、状态)。
