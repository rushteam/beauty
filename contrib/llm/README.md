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
- **`Retry(c, n, delay)`**:指数退避 + 随机 Jitter 重试(delay × 2^i × [0.5,1.5)),防雷群;流式已开始产出不重试。
- **`Metered(c, hook)`**:生成完成后回调用量与耗时——接哪(OTel/日志/账单)由你定,故本包不绑 OTel。
- **`Budget(c, maxTokens)`**:累计 token 预算;超限立即返回 `ErrBudgetExceeded`。`.Used()` / `.Remaining()` / `.Reset()` 观察与重置。
- **`Guard(c, checks...)`**:调下游前跑**输入**护栏,任一命中即拦截返回 `*GuardError`(见下)。
- **`GuardOutput(c, checks...)`**:模型回复后跑**输出**护栏;命中则 Generate 返错,Stream 推 Err chunk。

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

> 流式(`Stream`)支持文本增量推送与流式 tool_calls 解析(OpenAI / Anthropic 均已实现);
> `RunStream` 配任一 provider 都能推 token + 继续工具循环。provider 不支持时自动回退 `Generate`。

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
                return `{"temp":25,"cond":"晴"}`, nil
            }),
    },
}
resp, _ := r.Run(ctx, llm.Request{Model: "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "北京天气?"}}}).Final()
fmt.Println(resp.Content)

// 事件流:模型走 Stream 推 EventToken;工具轮结束后推 step / tool_* / final
ctx, cancel := context.WithCancel(ctx)
defer cancel()
for ev := range r.RunStream(ctx, req) {
    switch ev.Type {
    case agent.EventToken:
        fmt.Print(ev.Result) // 增量文本
    case agent.EventToolResult:
        log.Println(ev.ToolCall.Name, ev.Result)
    case agent.EventPaused:
        // out.Requirements / ev.RunID → Continue
    case agent.EventFinal:
        fmt.Println("\n>", ev.Response.Content)
    case agent.EventError:
        return ev.Err
    }
}
```

同轮多个 tool_call **默认并行**;串行:`ParallelTools: agent.Bool(false)`。
provider 若不支持流式 tool_calls,会自动回退 `Generate`。

工具来源与本包解耦:`agent.Tool.Call` 是普通函数,把 [`contrib/mcp`](../mcp) 的远程工具
(`session.CallTool`)适配成 `agent.Tool` 只需几行(见 [`contrib/mcpagent`](../mcpagent)),
故本包不 import mcp、保持零依赖。

### 工具权限三态 + 多 Agent 薄编排

- **Permission**:`PermitAllow`(默认) / `PermitAsk`(整轮原子暂停,待 `Continue`) / `PermitDeny`(策略拒绝)。
  旧字段 `Approval: true` 仍等价于 Ask。
- **AgentAsTool**:把子 `Agent`(任意实现,不限 `*Runner`)包成工具,父 agent 可委托子任务;子 Paused 会冒泡到父。
- **Chain**:按序跑多个 agent(上一步终态文本作为下一步输入);任一步 Paused 则整链暂停。
- 更完整的编排(统一 `Agent` 接口 / Planner / Team 移交 / BestOfN / VerifyLoop)见下一节。

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
out := chain.Run(ctx, req)
resp, _ := out.Final()
```

### 统一 Agent 接口 + 规划 / 团队 / 策略包装

`Runner` / `Chain` / `Team` / `Parallel` / `BestOfN` / `VerifyLoop` 都实现同一个 **`Agent`** 契约,可互相嵌套
(`AgentAsTool`、`Chain` 步骤、`Team` 成员都收 `Agent`,不再局限于 `*Runner`):

```go
type Agent interface {
    Run(ctx context.Context, req llm.Request) RunOutcome
    Continue(ctx context.Context, runID string, resolutions []Resolution) RunOutcome
    Info() Info
}
type StreamAgent interface {
    Agent
    RunStream(ctx context.Context, req llm.Request) <-chan Event
    ContinueStream(ctx context.Context, runID string, resolutions []Resolution) <-chan Event
}
```

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
`MinUnique` 判为 A↔B 打转),避免无限委托。`Team` 实现 `StreamAgent`,`RunStream` 会转发各成员事件
(带 `TriggerTransfer` 归因)、全程仅产出一条终态 `EventFinal`;它也可再被 `Chain` / `AgentAsTool` 嵌套。

```go
team := &agent.Team{
    Members: map[string]agent.Agent{"researcher": r1, "writer": r2},
    Entry:   "researcher",
    Config:  agent.HandoffConfig{MaxHandoffs: 8}, // 0 用默认
}
resp, _ := team.Run(ctx, req).Final() // researcher 可 "HANDOFF: writer ..." 移交给 writer
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
resp, _ := loop.Run(ctx, req).Final()
```

**`Parallel`(并发扇出 + 合并)**:把**不同的** Agent 并发跑同一 Request,再用可插拔 `Combiner` 合并
——补齐了组合家族里「并发」这一维度(`Chain` 串行、`Team` 路由、`BestOfN` 同 Agent 跑 N 次、
`Parallel` 不同 Agent 并发)。`Combine` 为 nil 用 `ConcatCombiner`(按序拼接);任一分支失败不影响其余,
全失败才报错。它实现 `StreamAgent`(透传各分支中间事件、仅产出一条合并后的终态),可再被 `Chain` 嵌套
(如「并发调研 → 单 agent 汇总」= `Chain{Parallel, summarizer}`)。

```go
p := &agent.Parallel{Agents: []agent.Agent{legal, finance, tech}} // 默认拼接
// 或注入自定义合并:投票 / 取最优 / 再交给一个模型综合
p.Combine = func(ctx context.Context, req llm.Request, cands []*llm.Response) (*llm.Response, error) { ... }
resp, _ := p.Run(ctx, req).Final()
```

**事件父子归因**:`RunStream` 的每条 `Event` 都带 `AgentName`(`Runner.Name`)与 `TriggerType`/`TriggerID`
(`TriggerUser`/`TriggerToolCall`/`TriggerTransfer`),多 agent 场景下可归因「由谁 / 因何触发」。
编排点(`AgentAsTool` / `Team`)在调子 agent 前用 `WithTrigger(ctx, tt, id)` 标注来源。

> 可运行示例见 [`agent/example_test.go`](agent/example_test.go)(`go test -run Example ./agent/`,离线 stub、无需 key)。

### 鲁棒性开关(工具参数容错 / 上下文压缩 / 消息合并)

三个 opt-in、纯确定性(不额外调模型)的开关,按需打开:

- **`RepairToolArgs`**:模型给出的 tool_call 参数常"几乎合法"(尾逗号、单引号、裸 key、被 ```围栏包住、
  JS/Py 常量、注释)。开启后,当参数 `json.Valid` 失败时先用 `agent.RepairJSON` 尽力修复,**修好且重新
  校验通过才**交给 `Tool.Call`,否则原样透传——绝不会把更坏的输入喂给工具。
- **`Compactor`**:工具密集的长跑里历史 tool 结果会撑爆上下文。`Runner.Compactor` 在每轮调用前对**发出的
  投影**做 token 估算 + 截断最旧的大 tool 结果(末尾 `KeepRecent` 条完整保留),规范历史保持完整、每轮重新
  投影。与 session 的跨轮滚动摘要互补(那个要调模型,这个不调)。
- **`MergeConsecutive` / `MergeMessagesHook`**:合并相邻同角色(system/user/assistant)消息(`Tool` 永不合并),
  用于某些要求角色严格交替的 provider,或注入 system/Steer/nudge 后规整请求。可直接当 `BeforeModel` Hook 挂。

```go
r := &agent.Runner{
    Client:         cli,
    Tools:          tools,
    RepairToolArgs: true,                                   // 容忍坏 JSON 参数
    Compactor:      &agent.Compactor{MaxTokens: 6000},      // 长跑压缩历史 tool 结果
    Hooks:          agent.Hooks{BeforeModel: agent.MergeMessagesHook("")}, // 规整消息
}
```

### 人工审批 / 暂停续跑(human-in-the-loop)

给敏感工具标 `Permission: agent.PermitAsk`(或旧 `Approval: true`)。同轮一旦出现 Ask,**整轮工具都不执行**
(原子暂停),`Run` 返回 `Status=paused` 与 `Requirements`。调用方决议后 `Continue`:

```go
r := &agent.Runner{Client: cli, Tools: []agent.Tool{{Def: ..., Call: ..., Permission: agent.PermitAsk}}}
out := r.Run(ctx, req)
if out.IsPaused() {
    var resolutions []agent.Resolution
    for _, rq := range out.Requirements {
        ok := askHuman(rq.ToolCall)
        resolutions = append(resolutions, agent.Resolution{ID: rq.ID, Approved: ok, Reason: "需确认"})
    }
    out = r.Continue(ctx, out.RunID, resolutions)
}
resp, err := out.Final()
```

需要进程内阻塞审批时,用可选适配器(不进 Runner 核心):

```go
a := agent.SyncHITL(r, func(ctx context.Context, tc llm.ToolCall) (agent.Resolution, error) {
    return agent.Resolution{Approved: askHuman(tc)}, nil
})
out := a.Run(ctx, req) // 内部自动 Continue 直到 Done/Error
```

### 会话记忆(`llm/agent/session`)

`session.Manager` 在任意 `Agent` 之上加多轮记忆:持久化对话历史,超长时滚动摘要。每轮只传新输入,
历史与摘要自动拼进请求。`Run` 收 `Agent`、`RunStream` 收 `StreamAgent`,故 `Runner` 之外的
`Chain` / `Team` / `BestOfN` / `VerifyLoop` 组合体也能套上会话记忆。内置 `MemoryStore` 与
**`FileStore`(JSON 落盘)**;生产 SQLite/Redis 见 [`contrib/llmsession`](../llmsession)。

```go
store, _ := session.NewFileStore("./data/sessions")
mgr := &session.Manager{Store: store,
    Summarizer: &session.Summarizer{Client: cli, Model: "gpt-4o-mini", MaxMessages: 20, KeepRecent: 6}}
out := mgr.Run(ctx, "session-123", r, llm.Request{Model: "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "接着上次说"}}})
resp, _ := out.Final()
```

Paused 时会话记下 `PendingRunID`,用 `mgr.Continue(ctx, id, a, resolutions)` 恢复。

详见 [`llm/agent/session`](agent/session)。也可用 `mgr.RunStream` 转发事件并在 `EventFinal` 时落盘。

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
steer := agent.NewSteer(8)
r := &agent.Runner{
    Client: cli, Tools: tools, Steer: steer,
    Hooks: agent.Hooks{
        BeforeModel: func(ctx context.Context, step int, req *llm.Request) error { return nil },
        AfterTool:   func(ctx context.Context, step int, tc llm.ToolCall, result string) error { return nil },
    },
}
steer.Enqueue("先别删文件,只列出来") // 下一轮 Generate 前注入为 user 消息
for ev := range r.RunStream(ctx, req) { /* EventSteer 可见插话 */ }
```

## Agent Skills(`llm/agent/skills`)

在 agent 循环之上实现 **Agent Skills**(与 Claude Code 的 `SKILL.md` 同规范):一个技能 =
一个目录(`SKILL.md` + 可选 `scripts/`、`references/`)。加载后以**渐进式披露**接入 `Runner`——
系统提示只放技能名录,模型命中任务时才按需拉全文/读引用/跑脚本。

```go
import "github.com/rushteam/beauty/contrib/llm/agent/skills"

sk, _ := skills.Load(skills.LocalSkills{Dir: "./skills"})
r := &agent.Runner{Client: cli, Tools: sk.Tools()} // 三个元工具:instructions/reference/script
resp, _ := r.Run(ctx, llm.Request{Model: "gpt-4o", System: sk.SystemPrompt(),
    Messages: []llm.Message{{Role: llm.User, Content: "..."}}}).Final()
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

### 便捷构造 + 链式配置

```go
prompt.SystemSlot("id", priority, "内容")                     // Position=System, Enabled=true
prompt.AfterSlot("id", llm.System, priority, "内容")          // Position=After
prompt.ChatSlot("id", llm.System, depth, priority, "内容")    // Position=Chat

// 链式设置可选属性
prompt.SystemSlot("planner", 100, reactInstr).
    When(func(ctx prompt.Context) bool { return ctx.Step == 1 }).
    WithMaxTokens(2000).
    WithSource("planner")
```

### Token 预算(两层裁剪)

- **`Slot.MaxTokens`**:单 slot 内容上限,超出时截断(二分查找精确裁剪)。
- **`Assembler.SystemBudget`**:所有 System slot 合计上限,超出时从低优先级整条丢弃。
- **`Assembler.TokenCounter`**:自定义计数器(nil 用 `utf8.RuneCountInString` 近似;生产接 tiktoken)。

### 增量模式

Assembler 采用增量模式:System slot 追加在已有 `req.System` 之后,与 Session/Planner 自然共存。
简单场景直接设 `req.System` 字符串即可,不需要 Assembler;当 prompt 来源 ≥3 个需要编排时才引入。

```go
// 只加 RAG,Planner/Session 仍自行工作
asm := prompt.New(prompt.AfterSlot("rag", llm.System, 0, ragResult))
runner := &agent.Runner{
    Planner: &agent.ReActPlanner{},
    Hooks:   agent.Hooks{BeforeModel: asm.Hook()},
}
```

**ChainHooks** 串联多个 `BeforeModel` hook(日志、Assembler、限流等按序执行):

```go
runner.Hooks.BeforeModel = prompt.ChainHooks(myLogger, asm.Hook(), myGuard)
```

**Snapshot** 返回已解析 slot 列表,便于调试/日志:

```go
resolved := asm.Snapshot(prompt.Context{Step: 1, MessageCount: 5})
for _, s := range resolved {
    fmt.Printf("[%s] %s p=%d: %s\n", s.Position, s.ID, s.Priority, s.Content[:40])
}
```

## 护栏(guardrails)

### 输入护栏

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

### 输出护栏

`GuardOutput` 检查模型**回复**内容;Generate 返错,Stream 推 Err chunk。与 Guard(输入)对称。

```go
safe := llm.GuardOutput(cli,
    llm.Toxic(),             // 内置有害/拒绝词表(可传自定义)
    llm.MaxOutputLen(8000),  // 输出长度上限
    llm.OutputRegexp("no_code", regexp.MustCompile(`(?i)rm\s+-rf`)), // 自定义正则
)
```

## 多模态(Vision)

`Message.Parts` 支持文本+图片混排(OpenAI vision / Anthropic multimodal),纯文本仍用 Content:

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

`Request.ResponseFormat` 控制输出格式。OpenAI 支持 `json_object` 和 `json_schema`(structured outputs):

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

`Budget` 限制累计 token 消耗;超限返回 `ErrBudgetExceeded`。适合测试/按用户限额:

```go
bc := llm.Budget(cli, 100000) // 10 万 token 上限
r := &agent.Runner{Client: bc, ...}
// bc.Used() / bc.Remaining() / bc.Reset()
```

## 响应缓存

`Cache` 对相同请求缓存 Response,避免重复 API 调用(省钱/降延迟)。默认仅缓存 temperature=0 的确定性请求;
Stream 场景:命中时回放为单 Chunk,未命中时正常流式并在 Done 后写缓存。

```go
store := llm.NewMemoryCacheStore(1024) // 最多 1024 条;或自实现 CacheStore 接 Redis
cc := llm.Cache(cli, store,
    llm.WithCacheTTL(30*time.Minute),
    llm.WithCacheFilter(func(r llm.Request) bool { return true }), // 自定义条件
)
r := &agent.Runner{Client: cc, ...}
fmt.Println(cc.Stats()) // {Hits: 12, Misses: 3}
```

## Chat Template / Instruct 格式(`llm/instruct`)

本地模型(Llama、Mistral、Qwen 等)各有不同的 chat template 格式。大部分 OpenAI 兼容服务端
(ollama、vLLM、llama.cpp)会自动处理,但以下两种场景需要客户端侧格式化:

1. **Completion 端点**:本地部署只暴露 `/v1/completions`(text completion),需要手动拼 prompt。
2. **Chat 端点但模板不对**:服务端内置 chat template 跟模型训练格式不匹配,需要客户端覆盖。

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
// 之后照常用——Generate/Stream 内部自动格式化 + 合并 stop tokens
resp, _ := cli.Generate(ctx, llm.Request{
    Model:  "meta-llama/Llama-3.1-8B-Instruct",
    System: "You are helpful.",
    Messages: []llm.Message{{Role: llm.User, Content: "hello"}},
})
```

内置模板:

| 模板 | 适用模型 | 格式示例 |
|---|---|---|
| `instruct.ChatML` | Qwen、Yi、通用 fine-tune | `<\|im_start\|>system\n...<\|im_end\|>` |
| `instruct.Llama3` | Meta Llama 3/3.1/3.2 | `<\|begin_of_text\|><\|start_header_id\|>system...` |
| `instruct.Mistral` | Mistral / Mixtral | `<s>[INST] ... [/INST]` |
| `instruct.Alpaca` | Stanford Alpaca / Vicuna | `### System:\n...\n### Instruction:\n...` |
| `instruct.Gemma` | Google Gemma | `<start_of_turn>user\n[System: ...]...` |

自定义模板:

```go
myTemplate := &instruct.Template{
    Name:            "my-model",
    BOS:             "<s>",
    SystemPrefix:    "<|system|>\n",
    SystemSuffix:    "</s>\n",
    UserPrefix:      "<|user|>\n",
    UserSuffix:      "</s>\n",
    AssistantPrefix: "<|assistant|>\n",
    AssistantSuffix: "</s>\n",
    StopStrings:     []string{"</s>"},
}
cli := openai.New(key, openai.WithCompletionMode(myTemplate))
```

也可直接用 `Template.Format(req)` 获取格式化后的纯文本(不经 provider):

```go
prompt := instruct.Llama3.Format(req) // 拿到格式化文本,自行发 HTTP
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
| **AWS Bedrock** | `bedrock.New(region)`(自实现 SigV4 + event-stream;一包多家族:Claude / Titan / Llama / Mistral) |

- **`llm/openai`**:`chat/completions` + `embeddings` + `images/generations` + `images/edits` + `audio/speech`;
  `WithBaseURL` 对接兼容厂商,`NewAzure`/`WithAzure`/`WithAPIKeyHeader` 覆盖 Azure 及自定义认证。
  实现 `llm.Client` + `llm.Embedder` + `ImageGenerator`/`ImageEditor`/`SpeechSynthesizer`。
- **`llm/anthropic`**:`/v1/messages`(`x-api-key` + `anthropic-version`)。实现 `llm.Client`。
- **`llm/bedrock`**:AWS Bedrock Runtime(`/model/{id}/invoke` 与 `/invoke-with-response-stream`)。
  一个 `Client` 按 model id 前缀选家族 codec,覆盖 Anthropic Claude(tools + 多模态 + 流式全能力)、
  Amazon Titan(文本 + embedding)、Meta Llama、Mistral。实现 `llm.Client`;选中支持向量的家族时亦
  实现 `llm.Embedder`。SigV4 签名与 event-stream 帧解码均自实现(纯 stdlib,不引 AWS SDK)。

> 认证是否兼容:上述"OpenAI 兼容"厂商都用 `Authorization: Bearer <key>`,故 `openai` provider 直接可用;
> Azure 用 `api-key` 头 + 独特 URL,故单独 `NewAzure`;Bedrock 用 AWS SigV4 + event-stream 二进制流,
> 机制完全不同,由 `llm/bedrock` 自实现签名与帧解码(见下)。

## AWS Bedrock(`llm/bedrock`)

一个 `bedrock.New(region)` 客户端即可跨四个模型家族——按 `Request.Model` 的 id 前缀自动选 codec,
无需换 provider。认证走 AWS SigV4,流式走 AWS event-stream 二进制帧,均自实现(纯 stdlib)。

```go
import "github.com/rushteam/beauty/contrib/llm/bedrock"

// 凭据默认取自 AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN(临时凭据)
cli := bedrock.New("us-east-1")
// 或显式覆盖:bedrock.New("us-east-1", bedrock.WithStaticCredentials(ak, sk, token))

resp, _ := cli.Generate(ctx, llm.Request{
    Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0", // model id 即 Bedrock 模型/推理 profile id
    System:   "You are concise.",
    Messages: []llm.Message{{Role: llm.User, Content: "hello"}},
})
```

各家族 model id 示例(跨区推理 profile 前缀 `us.` / `eu.` / `apac.` 等会自动识别):

| 家族 | model id 示例 | 能力 |
|---|---|---|
| Anthropic Claude | `anthropic.claude-3-5-sonnet-20241022-v2:0` | tools + 多模态 + 流式 tool_calls |
| Amazon Titan | `amazon.titan-text-express-v1` / `amazon.titan-embed-text-v2:0` | 文本生成 / embedding |
| Meta Llama | `meta.llama3-1-70b-instruct-v1:0` | 文本(官方 chat 模板) |
| Mistral | `mistral.mistral-large-2402-v1:0` | 文本(`[INST]` 模板) |

> 非 Claude 家族为纯文本:忽略 `Tools`,多轮消息按各家族官方 chat 模板拼成单 prompt,`Parts` 退化取文本。

**embedding**(Titan Embeddings,`WithEmbedModel` 可换):

```go
cli := bedrock.New("us-east-1") // 默认 embed 模型 amazon.titan-embed-text-v2:0
vecs, _ := cli.Embed(ctx, []string{"检索文本 A", "检索文本 B"}) // 实现 llm.Embedder,可配 contrib/vector
```

**与中间件/多 provider 组合**(都基于 `llm.Client`,天然兼容):

```go
// 主用 Bedrock Claude,挂了自动切原生 Anthropic,再切 OpenAI
cli := llm.Fallback(
    llm.Retry(bedrock.New("us-east-1"), 3, 200*time.Millisecond),
    anthropic.New(anthropicKey),
    openai.New(openaiKey),
)
```

其它选项:`WithHTTPClient`(自定义超时/代理)、`WithBaseURL`(VPC endpoint / 测试打桩)、
`WithCodec`(接入注册表外的自定义家族)。单测用 `httptest` + `WithBaseURL` 打桩,无需真实 AWS 凭据。

## 边界

prompt 工程、模型选择、温度、成本换算表、选哪些工具/护栏规则、要不要人工审批都是 policy;
配 [`contrib/vector`](../vector) 的 `Embedder` 即可搭 RAG;
配 [`contrib/mcp`](../mcp) 把远程工具接进 `agent.Runner`。单测用 httptest 打桩(Generate/Stream/
Embed/Fallback/Metered/工具往返/护栏)+ 假 Client 驱动 Runner,不需真实 API key。

**Anthropic 流式 tool_calls**:Stream 现在完整解析 `content_block_start(tool_use)` /
`input_json_delta` / `message_stop`,Done 时带回完整 `ToolCalls`。RunStream 配 Claude 也能推 token + 工具循环。
