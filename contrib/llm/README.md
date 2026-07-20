# contrib/llm —— provider 无关的 LLM 客户端(独立模块)

对话 / 流式 / embedding 的统一接口 + 失败切换、重试、用量计量中间件。**纯标准库、零外部依赖**
(各家 provider 用 HTTP 直连其 REST API,不引重型 SDK),不 import beauty 核心。

```bash
go get github.com/rushteam/beauty/contrib/llm@latest
```

## 用法

```go
import (
    "github.com/rushteam/beauty/contrib/llm"
    "github.com/rushteam/beauty/contrib/llm/openai"
    "github.com/rushteam/beauty/contrib/llm/anthropic"
)

cli := openai.New(os.Getenv("OPENAI_API_KEY"))        // 或 anthropic.New(...)
resp, _ := cli.Generate(ctx, llm.Request{
    Model:    "gpt-4o",
    System:   "You are concise.",
    Messages: []llm.Message{{Role: llm.User, Content: "hello"}},
})
fmt.Println(resp.Content, resp.Usage)

// 流式(配合 beauty 的 SSE 直接推给前端)
ch, _ := cli.Stream(ctx, req)
for c := range ch {
    if c.Err != nil { break }
    fmt.Print(c.Delta) // 增量 token
}
```

## 中间件(组合 llm.Client)

```go
// 主用 Anthropic,挂了自动切 OpenAI;各自再包重试与计量。
cli := llm.Metered(
    llm.Fallback(
        llm.Retry(anthropic.New(k1), 3, 200*time.Millisecond),
        openai.New(k2),
    ),
    func(ctx context.Context, model string, u llm.Usage, d time.Duration) {
        // 上报 token/成本/延迟:接 OTel、日志或账单系统(policy 由你定)
    },
)
```

- **`Fallback(clients...)`**:按序尝试,前者出错切下一个(跨 provider/模型高可用)。
- **`Retry(c, n, delay)`**:重试建立阶段错误(流式已开始产出则不重试)。
- **`Metered(c, hook)`**:生成完成后回调用量与耗时——接哪(OTel/日志/账单)由你定,故本包不绑 OTel。

## 多厂商适配

**大部分厂商提供 OpenAI 兼容端点,换 `WithBaseURL` 即用同一 `openai` provider,无需专门适配:**

```go
openai.New(key, openai.WithBaseURL(openai.BaseURLZhipu))     // 智谱 GLM
openai.New(key, openai.WithBaseURL(openai.BaseURLMoonshot))  // Kimi / 月之暗面
openai.New(key, openai.WithBaseURL(openai.BaseURLMiniMax))   // MiniMax
openai.New(key, openai.WithBaseURL(openai.BaseURLDashScope)) // 阿里通义千问(compatible-mode)
openai.New(key, openai.WithBaseURL(openai.BaseURLDeepSeek))  // DeepSeek
// 其它 OpenAI 兼容网关 / 本地模型(ollama/vLLM 等):自填 WithBaseURL 即可
```

| 厂商 | 接法 |
|---|---|
| OpenAI / 智谱 / Kimi / MiniMax / 通义千问 / DeepSeek / 兼容网关 | `openai.New(key, WithBaseURL(...))`(见上,已带常量) |
| **Azure OpenAI** | `openai.NewAzure(endpoint, deployment, apiVersion, key)`(api-key 头 + deployment 路径 + api-version) |
| **Anthropic** | `anthropic.New(key)`(原生 Messages API) |
| **AWS Bedrock** | 需独立适配(SigV4 签名 + 按模型报文),不在本模块——可另起 `llm/bedrock` |

- **`llm/openai`**:`chat/completions` + `embeddings`;`WithBaseURL` 对接兼容厂商,`NewAzure`/`WithAzure`/
  `WithAPIKeyHeader` 覆盖 Azure 及自定义认证。实现 `llm.Client` + `llm.Embedder`。
- **`llm/anthropic`**:`/v1/messages`(`x-api-key` + `anthropic-version`)。实现 `llm.Client`。

> 认证是否兼容:上述"OpenAI 兼容"厂商都用 `Authorization: Bearer <key>`,故 `openai` provider 直接可用;
> Azure 用 `api-key` 头 + 独特 URL,故单独 `NewAzure`;Bedrock 用 AWS SigV4,机制完全不同,需专门实现。

## 边界

prompt 工程、模型选择、温度、成本换算表都是 policy;工具调用(function calling)/多模态可按需扩展。
配 [`contrib/vector`](../vector) 的 `Embedder` 即可搭 RAG。单测用 httptest 打桩(Generate/Stream/
Embed/Fallback/Metered),不需真实 API key。
