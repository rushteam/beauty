# contrib/llm —— provider 无关的 LLM 客户端(独立模块)

对话 / 流式 / embedding / **工具调用(function calling)** 的统一接口 + 失败切换、重试、用量计量、
**输入护栏**中间件,外加一个薄 **agent 循环**(`llm/agent`)。**纯标准库、零外部依赖**
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
- **`Guard(c, checks...)`**:调下游前跑输入护栏,任一命中即拦截返回 `*GuardError`(见下)。

## 工具调用(function calling)

`Request.Tools`(`[]ToolDef`,入参用 JSON Schema)声明可调用工具,`Request.ToolChoice`
控制策略(`""`/`auto`/`none`/`required`/具体工具名);模型要求调用时,结果落在
`Response.ToolCalls`。回传工具结果用一条 `Role: llm.Tool` + `ToolCallID` 的消息。
OpenAI 与 Anthropic 的线上格式(`tool_calls` vs `content blocks`)由各 provider 自动翻译,
你只面对中立的 `ToolDef`/`ToolCall`。

```go
resp, _ := cli.Generate(ctx, llm.Request{
    Model:    "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "北京天气?"}},
    Tools:    []llm.ToolDef{{Name: "get_weather", Description: "查天气",
        Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)}},
})
for _, tc := range resp.ToolCalls { /* 执行 tc,把结果作为 Role:llm.Tool 消息回传 */ }
```

> v1 的流式(`Stream`)只透传文本增量,不解析流式 tool_calls 分片;工具循环走 `Generate`(见下)。

## agent 循环(`llm/agent`)

`agent.Runner` 把"模型→调工具→喂回结果→再生成"的循环自动化,直到模型给出终态文本或到达
`MaxSteps`(默认 8)。未知工具/工具出错会作为错误文本喂回让模型自愈,不中断循环。

```go
import "github.com/rushteam/beauty/contrib/llm/agent"

r := &agent.Runner{
    Client: cli, // 任意 llm.Client(可叠 Fallback/Retry/Metered/Guard)
    Tools: []agent.Tool{
        agent.Func("get_weather", "查天气",
            json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
            func(ctx context.Context, args json.RawMessage) (string, error) {
                // 解析 args,执行,返回喂回模型的文本
                return `{"temp":25,"cond":"晴"}`, nil
            }),
    },
}
resp, _ := r.Run(ctx, llm.Request{Model: "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "北京天气?"}}})
fmt.Println(resp.Content)
```

工具来源与本包解耦:`agent.Tool.Call` 是普通函数,把 [`contrib/mcp`](../mcp) 的远程工具
(`session.CallTool`)适配成 `agent.Tool` 只需几行(见 [`contrib/mcpagent`](../mcpagent)),
故本包不 import mcp、保持零依赖。

## Agent Skills(`llm/agent/skills`)

在 agent 循环之上实现 **Agent Skills**(与 Claude Code 的 `SKILL.md` 同规范):一个技能 =
一个目录(`SKILL.md` + 可选 `scripts/`、`references/`)。加载后以**渐进式披露**接入 `Runner`——
系统提示只放技能名录,模型命中任务时才按需拉全文/读引用/跑脚本。

```go
import "github.com/rushteam/beauty/contrib/llm/agent/skills"

sk, _ := skills.Load(skills.LocalSkills{Dir: "./skills"})
r := &agent.Runner{Client: cli, Tools: sk.Tools()} // 三个元工具:instructions/reference/script
resp, _ := r.Run(ctx, llm.Request{Model: "gpt-4o", System: sk.SystemPrompt(),
    Messages: []llm.Message{{Role: llm.User, Content: "..."}}})
```

脚本执行默认关闭(只读),`sk.EnableExec(30*time.Second)` 显式开启;文件访问带路径穿越防护。
详见 [`llm/agent/skills`](agent/skills)。

### 人工审批(human-in-the-loop)

给敏感工具标 `Approval: true`,并设 `Runner.Approve`:执行前先过审批门。返回 `Approved:false`
把拒绝理由喂回模型继续;返回 error 视为审批失败、中止整个 Run。`Approve` 可阻塞等待人工确认
(从 channel / HTTP 拿决定)。

```go
r := &agent.Runner{
    Client: cli,
    Tools:  []agent.Tool{{Def: ..., Call: ..., Approval: true}}, // 敏感工具
    Approve: func(ctx context.Context, tc llm.ToolCall) (agent.Decision, error) {
        ok := askHuman(tc) // 你的确认逻辑(可阻塞)
        return agent.Decision{Approved: ok, Reason: "需管理员确认"}, nil
    },
}
```

### 会话记忆(`llm/agent/session`)

`session.Manager` 在 `Runner` 之上加多轮记忆:持久化对话历史,超长时滚动摘要。每轮只传新输入,
历史与摘要自动拼进请求。`Store` 接口 + 内置 `MemoryStore`(生产可换 sqldb/redis)。

```go
mgr := &session.Manager{Store: session.NewMemoryStore(),
    Summarizer: &session.Summarizer{Client: cli, Model: "gpt-4o-mini", MaxMessages: 20, KeepRecent: 6}}
resp, _ := mgr.Run(ctx, "session-123", r, llm.Request{Model: "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "接着上次说"}}})
```

详见 [`llm/agent/session`](agent/session)。

## 输入护栏(guardrails)

`Guard` 与其它中间件同构,叠在任意 client 外;被 `Runner` 使用时,工具循环里**每个模型回合都会先过检查**。
内置检查的匹配规则是 policy,可传参覆盖或自写 `Check`。仅检查用户可控文本(user/assistant),
不含 System 与工具结果。

```go
safe := llm.Guard(cli,
    llm.PromptInjection(),   // 越狱/注入措辞(内置中英词表,可传自定义整表替换)
    llm.PII(),               // 邮箱/卡号/手机号(内置正则,可传 *regexp.Regexp 覆盖)
    llm.MaxInputLen(4000),   // 输入长度上限
)
// 命中返回 *llm.GuardError{Check, Reason},下游不被调用
```

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

prompt 工程、模型选择、温度、成本换算表、选哪些工具/护栏规则、要不要人工审批都是 policy;
多模态、流式工具调用、输出护栏可按需扩展。配 [`contrib/vector`](../vector) 的 `Embedder` 即可搭 RAG;
配 [`contrib/mcp`](../mcp) 把远程工具接进 `agent.Runner`。单测用 httptest 打桩(Generate/Stream/
Embed/Fallback/Metered/工具往返/护栏)+ 假 Client 驱动 Runner,不需真实 API key。
