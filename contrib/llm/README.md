# contrib/llm —— provider 无关的 LLM 客户端(独立模块)

对话 / 流式 / embedding / **工具调用(function calling)** 的统一接口 + 失败切换、重试、用量计量、
**输入护栏**中间件,外加一个薄 **agent 循环**(`llm/agent`)。**纯标准库、零外部依赖**
(各家 provider 用 HTTP 直连其 REST API,不引重型 SDK),不 import beauty 核心。

```bash
go get github.com/rushteam/beauty/contrib/llm@latest
```

## 核心接口

整个模块围绕两个方法构建:

```go
type Client interface {
    Generate(ctx context.Context, req Request) (*Response, error)       // 非流式
    Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error]    // 流式迭代器
}
```

流式基于 Go 1.22+ 的 `iter.Seq2` 迭代器——天然背压(`break` 即停、无 goroutine 泄漏)、
零额外 goroutine、可直接 `for-range` 消费:

```go
type Chunk struct {
    Delta         string          // 增量文本
    ToolCalls     []ToolCall      // 流式工具调用(结束时填充完整列表)
    Usage         *Usage          // 最后一个 chunk 带最终用量
    ThinkingDelta string          // 思考过程增量(extended thinking)
    Thinking      string          // 完整思考文本(结束时)
}
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

// 流式
for chunk, err := range cli.Stream(ctx, req) {
    if err != nil { break }
    fmt.Print(chunk.Delta) // 增量 token
}

// 流式 → 完整 Response(不需要逐 chunk 消费时)
resp, err := llm.Collect(cli.Stream(ctx, req))
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
- **`Retry(c, n, delay)`**:指数退避 + 随机 Jitter 重试(delay × 2^i × [0.5,1.5)),防雷群;流式已开始产出不重试。
- **`Metered(c, hook)`**:生成完成后回调用量与耗时——接哪(OTel/日志/账单)由你定,故本包不绑 OTel。
- **`Budget(c, maxTokens)`**:累计 token 预算;超限立即返回 `ErrBudgetExceeded`。`.Used()` / `.Remaining()` / `.Reset()` 观察与重置。
- **`Guard(c, checks...)`**:调下游前跑**输入**护栏,任一命中即拦截返回 `*GuardError`(见下)。
- **`GuardOutput(c, checks...)`**:模型回复后跑**输出**护栏;命中则 Generate 返错,Stream yield error。

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

> 流式(`Stream`)支持文本增量推送与流式 tool_calls 解析(OpenAI / Anthropic 均已实现)。
> provider 不支持时自动回退 `Generate`。

## agent 循环(`llm/agent`)

`agent.Runner` 把"模型→调工具→喂回结果→再生成"的循环自动化,直到模型给出终态文本或到达
`MaxSteps`(默认 8)。未知工具/工具出错会作为错误文本喂回让模型自愈,不中断循环。

### 统一的 Agent 接口

所有编排原语(Runner / Chain / Team / Parallel / BestOfN / VerifyLoop)实现同一个
`Agent` 契约,可互相嵌套:

```go
type Agent interface {
    Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error]
    Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error]
    Info() Info
}
```

返回统一的 `iter.Seq2[Event, error]`——流式消费 `for-range`、非流式用 `CollectOutcome` 收束:

```go
import "github.com/rushteam/beauty/contrib/llm/agent"

r := &agent.Runner{
    Client: cli, // 任意 llm.Client(可叠 Fallback/Retry/Metered/Guard)
    Tools: []agent.Tool{
        agent.Func("get_weather", "查天气",
            json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
            func(ctx context.Context, args json.RawMessage) (string, error) {
                return `{"temp":25,"cond":"晴"}`, nil
            }),
    },
}

// 方式一:流式消费事件
for ev, err := range r.Run(ctx, llm.Request{Model: "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "北京天气?"}}}) {
    if err != nil { return err }
    switch ev.Type {
    case agent.EventToken:
        fmt.Print(ev.Result) // 增量文本
    case agent.EventToolResult:
        log.Println(ev.ToolCall.Name, ev.Result)
    case agent.EventFinal:
        fmt.Println("\n>", ev.Response.Content)
    case agent.EventPaused:
        // ev.Requirements / ev.RunID → Continue
    case agent.EventError:
        return ev.Err
    }
}

// 方式二:不需要事件流,直接拿结果
out := agent.CollectOutcome(r.Run(ctx, req))
resp, err := out.Final()
fmt.Println(resp.Content)
```

同轮多个 tool_call **默认并行**;串行:`ParallelTools: agent.Bool(false)`。

工具来源与本包解耦:`agent.Tool.Call` 是普通函数,把 [`contrib/mcp`](../mcp) 的远程工具
(`session.CallTool`)适配成 `agent.Tool` 只需几行(见 [`contrib/mcpagent`](../mcpagent)),
故本包不 import mcp、保持零依赖。

### Per-run Options(类型安全覆盖)

`Run` 的 `opts ...Option` 支持 per-run 覆盖,不侵入 Runner 结构:

```go
// 本次用 gpt-4o,最多跑 16 步,追加临时工具
for ev, err := range r.Run(ctx, req,
    agent.WithModel("gpt-4o"),
    agent.WithMaxSteps(16),
    agent.WithTemperature(0.7),
    agent.WithTools(extraTools),
) { ... }
```

可用选项:`WithModel`、`WithSystem`、`WithMaxSteps`、`WithTemperature`、`WithTools`、
`WithResponseFormat`。泛型 `GetOption[T]` 查找,多个同类型 last-wins。

### 双层中间件体系

**Agent 级中间件**(`AgentMiddleware`)包裹整个 run 流程,与 Provider 级(Client 装饰器)分层:

```go
// Agent 级:整轮 agent 运行的 wrap-around(日志、评估、重试、OTel 等)
r := &agent.Runner{
    Client: cli,
    Tools:  tools,
    Middlewares: []agent.AgentMiddleware{
        agent.LoggingMiddleware(slog.Info),
        agent.OTelMiddleware(otelCfg),
        agent.EvaluatorMiddleware(evalFunc, 3),
    },
}
```

| 层 | 作用域 | 典型用途 |
|---|---|---|
| **Provider 级**(`llm.Client` 装饰器) | 每次模型调用 | Fallback / Retry / Guard / Budget / Cache |
| **Agent 级**(`AgentMiddleware`) | 整轮 agent 运行 | 日志 / OTel trace / Evaluator loop / 来源标记 |
| **Hooks**(细粒度回调) | 步级 waterfall | BeforeModel / AfterModel / OnChunk / BeforeTool / AfterTool |

内置 Agent 中间件:
- `LoggingMiddleware`:运行前后记录请求/响应摘要
- `EvaluatorMiddleware`:运行后用评估函数检查,不达标则带反馈重跑
- `SourceAttributionMiddleware`:自动给事件标记 AgentName
- `OTelMiddleware`:OpenTelemetry GenAI 语义约定 trace + metrics

### History / Context Provider 分离

把**历史管理**和**上下文注入**解耦为独立接口:

```go
r := &agent.Runner{
    Client:   cli,
    Tools:    tools,
    // 历史:自动加载/持久化会话历史
    HistoryProv: agent.NewInMemoryHistoryProvider(),
    // 上下文:RAG 检索注入、Skills 注入等
    ContextProvs: []agent.ContextProvider{
        agent.RAGContextProvider(myRetriever),
    },
    SessionID: "session-123",
}
```

| 接口 | 职责 | 调用时机 |
|---|---|---|
| `HistoryProvider` | 会话历史的加载与持久化 | `Invoking` 前插历史 → `Invoked` 持久化 |
| `ContextProvider` | RAG / Skills / 环境等临时上下文 + 工具 | `Invoking` 追加上下文 → `Invoked` 可选回写 |

消息自动标记 `Source`(SourceHistory/SourceContext/SourceModel/SourceTool...),
持久化时按来源过滤——避免 history 消息被重复存储。

### 工具权限三态 + 多 Agent 薄编排

- **Permission**:`PermitAllow`(默认) / `PermitAsk`(整轮原子暂停,待 `Continue`) / `PermitDeny`(策略拒绝)。
- **AgentAsTool**:把子 `Agent`(任意实现,不限 `*Runner`)包成工具,父 agent 可委托子任务;子 Paused 会冒泡到父。
- **Chain**:按序跑多个 agent(上一步终态文本作为下一步输入);任一步 Paused 则整链暂停。

```go
sub := &agent.Runner{Client: cli, Tools: researchTools}
parent := &agent.Runner{Client: cli, Tools: []agent.Tool{
    agent.AgentAsTool("research", "调研子任务", sub, agent.WithAgentToolModel("gpt-4o")),
    {Def: dangerDef, Call: dangerCall, Permission: agent.PermitAsk},
}}

chain := &agent.Chain{Steps: []agent.ChainStep{
    {Name: "draft", Runner: drafter, Model: "gpt-4o"},
    {Name: "review", Runner: reviewer, Model: "gpt-4o", System: "严格审稿"},
}}
out := agent.CollectOutcome(chain.Run(ctx, req))
resp, _ := out.Final()
```

### 规划 / 团队 / 策略包装

`RunOutcome.Status` 为 `done` / `paused` / `error`。`out.Final()` 在 Done 时返回 `*Response`,Paused 时返回 `ErrPaused`。

**Planner 接缝 + ReActPlanner**:给 `Runner.Planner` 赋值即在首轮前注入规划指令、并对每轮响应做后处理。
`ReActPlanner` 让模型按 `/*PLANNING*/`→`/*REASONING*/`/`/*ACTION*/` 组织输出、以 `FINAL ANSWER:` 收尾,
并把终态文本收敛为干净答复(纯字符串处理,零配置可用,标记可覆盖)。

```go
r := &agent.Runner{Client: cli, Planner: &agent.ReActPlanner{}}
// 模型输出 ".../*PLANNING*/...\nFINAL ANSWER: 42" → resp.Content 收敛为 "42"
```

**Team(多 agent 移交)**:成员在终态文本里写 `HANDOFF: <成员名> <输入>` 即把控制权交给同伴;无标记即终态。
每次移交都过 **loop-safety 护栏**——`MaxHandoffs`(默认 16)上限 + 滑动窗口重复检测(窗口内不同目标数 <
`MinUnique` 判为 A↔B 打转),避免无限委托。

```go
team := &agent.Team{
    Members: map[string]agent.Agent{"researcher": r1, "writer": r2},
    Entry:   "researcher",
    Config:  agent.HandoffConfig{MaxHandoffs: 8},
}
out := agent.CollectOutcome(team.Run(ctx, req))
resp, _ := out.Final() // researcher 可 "HANDOFF: writer ..." 移交给 writer
```

**策略包装器**(在任意 `Agent` 之上再包一层运行策略,判定/校验逻辑是 policy,由你注入):

- **`BestOfN`**:并行跑 N 次,用 `Selector` 选最佳(默认 `LongestSelector`;可换成让另一个模型打分)。
- **`VerifyLoop`**:Ralph 式「跑→校验→带反馈重跑」,直到 `Verifier` 通过或到 `MaxRounds`(默认 3)。

```go
best := &agent.BestOfN{Agent: r, N: 3}                 // Select 为 nil → 选最长
loop := &agent.VerifyLoop{Agent: best, MaxRounds: 3,   // 包装器可任意嵌套
    Verify: func(ctx context.Context, resp *llm.Response) (ok bool, feedback string, err error) {
        return check(resp.Content) // 跑断言 / bash / 再问模型……都行
    }}
out := agent.CollectOutcome(loop.Run(ctx, req))
resp, _ := out.Final()
```

**`Parallel`(并发扇出 + 合并)**:把**不同的** Agent 并发跑同一 Request,再用可插拔 `Combiner` 合并
——补齐了组合家族里「并发」这一维度(`Chain` 串行、`Team` 路由、`BestOfN` 同 Agent 跑 N 次、
`Parallel` 不同 Agent 并发)。

```go
p := &agent.Parallel{Agents: []agent.Agent{legal, finance, tech}} // 默认拼接
// 或注入自定义合并:投票 / 取最优 / 再交给一个模型综合
p.Combine = func(ctx context.Context, req llm.Request, cands []*llm.Response) (*llm.Response, error) { ... }
out := agent.CollectOutcome(p.Run(ctx, req))
resp, _ := out.Final()
```

**事件父子归因**:每条 `Event` 都带 `AgentName`(`Runner.Name`)与 `TriggerType`/`TriggerID`
(`TriggerUser`/`TriggerToolCall`/`TriggerTransfer`),多 agent 场景下可归因「由谁 / 因何触发」。

> 可运行示例见 [`agent/example_test.go`](agent/example_test.go)(`go test -run Example ./agent/`,离线 stub、无需 key)。

### 工作流图引擎(`llm/agent/workflow`)

声明式图编排,适合条件分支、循环审批、动态路由等用 Chain/Team 难以表达的复杂场景:

```go
import "github.com/rushteam/beauty/contrib/llm/agent/workflow"

w, _ := workflow.NewBuilder("support-flow").
    AddNode("classify", workflow.LLMNode(cli, "gpt-4o-mini", "判断用户问题类型")).
    AddNode("tech", workflow.AgentNode(techAgent, "gpt-4o", "技术支持")).
    AddNode("sales", workflow.AgentNode(salesAgent, "gpt-4o", "销售咨询")).
    AddEdge(workflow.StartNode, "classify").
    AddConditionalEdge("classify", map[string]workflow.NodeID{
        "tech":  "tech",
        "sales": "sales",
    }).
    AddEdge("tech", workflow.EndNode).
    AddEdge("sales", workflow.EndNode).
    Build()

engine := workflow.NewEngine(w, workflow.WithMaxSteps(20))
for ev, err := range engine.RunIter(ctx, req) {
    if err != nil { break }
    fmt.Println(ev.Type, ev.Result)
}
```

核心概念:
- **Node**:执行单元(agent 调用、LLM 调用、条件判断、数据变换)
- **Edge**:节点连接(直连、条件路由、扇出/扇入)
- **State**:类型安全的可变状态容器,在节点间传递
- **Engine**:执行器,支持 `Run`(同步) / `RunIter`(迭代器) / `WithCheckpoint`(检查点回调)

内置节点工厂:`AgentNode`、`LLMNode`、`ConditionNode`、`TransformNode`。

### OTel 可观测性(`OTelMiddleware`)

遵循 OpenTelemetry GenAI 语义约定的 agent 中间件。本包不直接依赖 OTel SDK——通过回调接口注入:

```go
otelCfg := agent.OTelConfig{
    Tracer:  myOTelTracer,   // 实现 agent.Tracer 接口
    Metrics: myOTelMetrics,  // 实现 agent.MetricRecorder 接口
}
r := &agent.Runner{
    Client:      cli,
    Tools:       tools,
    Middlewares: []agent.AgentMiddleware{agent.OTelMiddleware(otelCfg)},
}
```

自动 trace:agent 整轮运行、每步模型调用、每次工具执行。自动 metrics:耗时、token 用量、工具调用计数。

### 结构化输出(`llm.TypedOutput`)

从 Go 类型自动生成 JSON Schema,类型安全地反序列化模型响应:

```go
type Review struct {
    Score   int    `json:"score"`
    Summary string `json:"summary"`
}

out, _ := llm.NewTypedOutput[Review]("review")
req := llm.Request{Model: "gpt-4o", Messages: msgs, ResponseFormat: out.Format()}
resp, _ := cli.Generate(ctx, req)
review, _ := out.Unmarshal(resp) // review 是 Review 类型
```

### 鲁棒性开关(工具参数容错 / 上下文压缩 / 消息合并)

三个 opt-in、纯确定性(不额外调模型)的开关,按需打开:

- **`RepairToolArgs`**:模型给出的 tool_call 参数常"几乎合法"(尾逗号、单引号、裸 key、被 ``` 围栏包住、
  JS/Py 常量、注释)。开启后,当参数 `json.Valid` 失败时先用 `agent.RepairJSON` 尽力修复。
- **`Compaction`**:可插拔的上下文压缩策略(`compaction.Strategy`),在每轮模型调用前压缩历史。
  内置策略:`SlidingWindow`(滑动窗口)、`Truncation`(截断)、`ToolResults`(压缩工具结果)、
  `Summarization`(摘要,需调模型)。
- **`MergeConsecutive` / `MergeMessagesHook`**:合并相邻同角色消息,用于要求角色严格交替的 provider。

```go
r := &agent.Runner{
    Client:         cli,
    Tools:          tools,
    RepairToolArgs: true,
    Compaction:     compaction.NewSlidingWindow(6000, 4),
    Hooks:          agent.Hooks{BeforeModel: agent.MergeMessagesHook("")},
}
```

### 人工审批 / 暂停续跑(human-in-the-loop)

给敏感工具标 `Permission: agent.PermitAsk`。同轮一旦出现 Ask,**整轮工具都不执行**
(原子暂停),事件流产出 `EventPaused`。调用方决议后 `Continue`:

```go
r := &agent.Runner{Client: cli, Tools: []agent.Tool{{Def: ..., Call: ..., Permission: agent.PermitAsk}}}
out := agent.CollectOutcome(r.Run(ctx, req))
if out.IsPaused() {
    var resolutions []agent.Resolution
    for _, rq := range out.Requirements {
        ok := askHuman(rq.ToolCall)
        resolutions = append(resolutions, agent.Resolution{ID: rq.ID, Approved: ok, Reason: "需确认"})
    }
    out = agent.CollectOutcome(r.Continue(ctx, out.RunID, resolutions))
}
resp, err := out.Final()
```

需要进程内阻塞审批时,用可选适配器(不进 Runner 核心):

```go
a := agent.SyncHITL(r, func(ctx context.Context, tc llm.ToolCall) (agent.Resolution, error) {
    return agent.Resolution{Approved: askHuman(tc)}, nil
})
out := agent.CollectOutcome(a.Run(ctx, req)) // 内部自动 Continue 直到 Done/Error
```

### 会话记忆(`llm/agent/session`)

`session.Manager` 在任意 `Agent` 之上加多轮记忆:持久化对话历史,超长时滚动摘要。
内置 `MemoryStore` 与 **`FileStore`(JSON 落盘)**;生产 SQLite/Redis 见 [`contrib/llmsession`](../llmsession)。

```go
store, _ := session.NewFileStore("./data/sessions")
mgr := &session.Manager{Store: store,
    Summarizer: &session.Summarizer{Client: cli, Model: "gpt-4o-mini", MaxMessages: 20, KeepRecent: 6}}
out := mgr.Run(ctx, "session-123", r, llm.Request{Model: "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "接着上次说"}}})
resp, _ := out.Final()
```

### 长期记忆工具(`llm/agent/memory`)

跨会话的薄记忆:`memory_add` / `memory_search` / `memory_delete`,默认可挂到 `Runner.Tools`。
内存实现用子串检索;语义检索用 [`contrib/memoryvector`](../memoryvector)(Embedder + vector)。

```go
import (
    "github.com/rushteam/beauty/contrib/llm/agent/memory"
    "github.com/rushteam/beauty/contrib/memoryvector"
    "github.com/rushteam/beauty/contrib/vector"
)

mem, _ := memoryvector.New(openai.New(key), vector.NewMemoryStore())
r.Tools = append(r.Tools, memory.Tools(mem, "user-42")...)
```

### 中途插话 + Hooks

```go
mailbox := agent.NewMailbox(8)
r := &agent.Runner{
    Client: cli, Tools: tools, Mailbox: mailbox,
    Hooks: agent.Hooks{
        BeforeModel: func(ctx context.Context, step int, req *llm.Request) error { return nil },
        AfterTool:   func(ctx context.Context, step int, tc llm.ToolCall, result *string) error { return nil },
    },
}
mailbox.Steer("先别删文件,只列出来") // 下一轮 Generate 前注入为 user 消息
for ev, err := range r.Run(ctx, req) { /* EventSteer 可见插话 */ }
```

## Agent Skills(`llm/agent/skills`)

在 agent 循环之上实现 **Agent Skills**(与 Claude Code 的 `SKILL.md` 同规范):一个技能 =
一个目录(`SKILL.md` + 可选 `scripts/`、`references/`)。加载后以**渐进式披露**接入 `Runner`——
系统提示只放技能名录,模型命中任务时才按需拉全文/读引用/跑脚本。

```go
import "github.com/rushteam/beauty/contrib/llm/agent/skills"

sk, _ := skills.Load(skills.LocalSkills{Dir: "./skills"})
r := &agent.Runner{Client: cli, Tools: sk.Tools()} // 三个元工具:instructions/reference/script
out := agent.CollectOutcome(r.Run(ctx, llm.Request{Model: "gpt-4o", System: sk.SystemPrompt(),
    Messages: []llm.Message{{Role: llm.User, Content: "..."}}}))
resp, _ := out.Final()
```

脚本执行默认关闭(只读),`sk.EnableExec(30*time.Second)` 显式开启;文件访问带路径穿越防护。
详见 [`llm/agent/skills`](agent/skills)。

## Prompt 组装协议(`llm/prompt`)

把散落在各层(人设、技能目录、规划指令、会话摘要、RAG 上下文、护栏…)的 prompt 片段统一声明为
**Slot**,由 `Assembler` 按 `Position→Priority` 规则组装成最终的 `llm.Request`。

```go
import "github.com/rushteam/beauty/contrib/llm/prompt"

asm := prompt.New(
    prompt.SystemSlot("persona", 0, "你是一个专业助手"),
    prompt.SystemSlot("skills", 50, "").Dynamic(func(prompt.Context) string { return sk.SystemPrompt() }),
    prompt.SystemSlot("planner", 100, reactInstr).When(func(ctx prompt.Context) bool { return ctx.Step == 1 }),
    prompt.AfterSlot("rag", llm.System, 0, "").Dynamic(ragLookup).WithMaxTokens(2000),
    prompt.AfterSlot("guardrail", llm.System, 10, "不要泄露内部信息"),
)
asm.SystemBudget = 4000  // System slot 合计 token 上限

runner := &agent.Runner{
    Client: cli,
    Hooks:  agent.Hooks{BeforeModel: prompt.ChainHooks(myLogger, asm.Hook())},
}
```

### 四个 Position

| Position | 放置位置 | 典型用途 |
|---|---|---|
| `System` | 合并进 `Request.System` | 人设、技能目录、规划指令、摘要 |
| `Before` | 消息列表最前面 | 历史前置上下文 |
| `After` | 最后一条 user 消息之前 | RAG 结果、护栏指令 |
| `Chat` | 按 `Depth` 从末尾往上插入 | 实时插话、微调 |

## 护栏(guardrails)

### 输入护栏

`Guard` 与其它中间件同构,叠在任意 client 外;被 `Runner` 使用时,工具循环里**每个模型回合都会先过检查**。

```go
safe := llm.Guard(cli,
    llm.PromptInjection(),   // 越狱/注入措辞
    llm.PII(),               // 邮箱/卡号/手机号
    llm.MaxInputLen(4000),   // 输入长度上限
)
```

### 输出护栏

```go
safe := llm.GuardOutput(cli,
    llm.Toxic(),             // 有害/拒绝词表
    llm.MaxOutputLen(8000),  // 输出长度上限
    llm.OutputRegexp("no_code", regexp.MustCompile(`(?i)rm\s+-rf`)),
)
```

## 多模态(Vision)

```go
resp, _ := cli.Generate(ctx, llm.Request{
    Model: "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Parts: []llm.Part{
        {Type: llm.PartText, Text: "这张图片里有什么?"},
        {Type: llm.PartImage, ImageURL: "https://example.com/cat.jpg", Detail: "high"},
    }}},
})
```

## Structured Output (JSON mode)

```go
resp, _ := cli.Generate(ctx, llm.Request{
    Model: "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "列出3种水果"}},
    ResponseFormat: &llm.ResponseFormat{
        Type: "json_schema",
        JSONSchema: &llm.JSONSchema{
            Name: "fruits",
            Schema: json.RawMessage(`{"type":"object","properties":{"fruits":{"type":"array","items":{"type":"string"}}},"required":["fruits"]}`),
            Strict: true,
        },
    },
})
```

## Token 预算

```go
bc := llm.Budget(cli, 100000) // 10 万 token 上限
r := &agent.Runner{Client: bc, ...}
// bc.Used() / bc.Remaining() / bc.Reset()
```

## 响应缓存

```go
store := llm.NewMemoryCacheStore(1024) // 最多 1024 条;或自实现 CacheStore 接 Redis
cc := llm.Cache(cli, store,
    llm.WithCacheTTL(30*time.Minute),
    llm.WithCacheFilter(func(r llm.Request) bool { return true }),
)
r := &agent.Runner{Client: cc, ...}
fmt.Println(cc.Stats()) // {Hits: 12, Misses: 3}
```

## Chat Template / Instruct 格式(`llm/instruct`)

本地模型(Llama、Mistral、Qwen 等)各有不同的 chat template 格式。

```go
import (
    "github.com/rushteam/beauty/contrib/llm/openai"
    "github.com/rushteam/beauty/contrib/llm/instruct"
)

// 走 /v1/completions 端点,用 Llama 3 chat template 格式化
cli := openai.New(key,
    openai.WithBaseURL("http://localhost:8080/v1"),
    openai.WithCompletionMode(&instruct.Llama3),
)
```

内置模板:

| 模板 | 适用模型 | 格式示例 |
|---|---|---|
| `instruct.ChatML` | Qwen、Yi、通用 fine-tune | `<\|im_start\|>system\n...<\|im_end\|>` |
| `instruct.Llama3` | Meta Llama 3/3.1/3.2 | `<\|begin_of_text\|><\|start_header_id\|>system...` |
| `instruct.Mistral` | Mistral / Mixtral | `<s>[INST] ... [/INST]` |
| `instruct.Alpaca` | Stanford Alpaca / Vicuna | `### System:\n...\n### Instruction:\n...` |
| `instruct.Gemma` | Google Gemma | `<start_of_turn>user\n[System: ...]...` |

## 多厂商适配

**大部分厂商提供 OpenAI 兼容端点,换 `WithBaseURL` 即用同一 `openai` provider,无需专门适配:**

```go
openai.New(key, openai.WithBaseURL(openai.BaseURLZhipu))     // 智谱 GLM
openai.New(key, openai.WithBaseURL(openai.BaseURLMoonshot))  // Kimi / 月之暗面
openai.New(key, openai.WithBaseURL(openai.BaseURLMiniMax))   // MiniMax
openai.New(key, openai.WithBaseURL(openai.BaseURLDashScope)) // 阿里通义千问(compatible-mode)
openai.New(key, openai.WithBaseURL(openai.BaseURLDeepSeek))  // DeepSeek
```

| 厂商 | 接法 |
|---|---|
| OpenAI / 智谱 / Kimi / MiniMax / 通义千问 / DeepSeek / 兼容网关 | `openai.New(key, WithBaseURL(...))` |
| **Azure OpenAI** | `openai.NewAzure(endpoint, deployment, apiVersion, key)` |
| **Anthropic** | `anthropic.New(key)`(原生 Messages API) |
| **AWS Bedrock** | `bedrock.New(region)`(SigV4 + event-stream;Claude / Titan / Llama / Mistral) |

## AWS Bedrock(`llm/bedrock`)

一个 `bedrock.New(region)` 客户端即可跨四个模型家族——按 `Request.Model` 的 id 前缀自动选 codec。

```go
import "github.com/rushteam/beauty/contrib/llm/bedrock"

cli := bedrock.New("us-east-1")
resp, _ := cli.Generate(ctx, llm.Request{
    Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
    System:   "You are concise.",
    Messages: []llm.Message{{Role: llm.User, Content: "hello"}},
})
```

## 边界

prompt 工程、模型选择、温度、成本换算表、选哪些工具/护栏规则、要不要人工审批都是 policy;
配 [`contrib/vector`](../vector) 的 `Embedder` 即可搭 RAG;
配 [`contrib/mcp`](../mcp) 把远程工具接进 `agent.Runner`。单测用 httptest 打桩(Generate/Stream/
Embed/Fallback/Metered/工具往返/护栏)+ 假 Client 驱动 Runner,不需真实 API key。
