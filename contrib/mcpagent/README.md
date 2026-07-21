# contrib/mcpagent —— MCP 工具 → agent.Tool 桥接(独立模块)

把 [`contrib/mcp`](../mcp) 客户端会话上的**远程 MCP 工具**桥接成 [`contrib/llm/agent`](../llm/agent)
的 `Tool`,于是 MCP server 暴露的工具能直接喂给 `agent.Runner` 驱动的 LLM 工具循环:

```
模型请求调用工具 → 桥接转发到 MCP server → 文本结果回传给模型 → 继续
```

这是刻意独立的**胶水模块**:它同时依赖 `mcp` 与 `llm/agent`,好让那两个模块彼此**零耦合**
(`llm` 不 import `mcp`、`mcp` 不 import `llm`,各自可单独用)。

## 用法

```go
import (
    "github.com/rushteam/beauty/contrib/mcp"
    "github.com/rushteam/beauty/contrib/llm"
    "github.com/rushteam/beauty/contrib/llm/agent"
    "github.com/rushteam/beauty/contrib/llm/openai"
    "github.com/rushteam/beauty/contrib/mcpagent"
)

// 1) 连到一个 MCP server(远程 HTTP / 本地子进程 / 进程内均可)
sess, _ := mcp.DialHTTP(ctx, "app", "https://tools.example.com/mcp")
defer sess.Close()

// 2) 把它的工具桥接成 agent.Tool(名字/描述/入参 schema 自动透传)
tools, _ := mcpagent.Tools(ctx, sess)

// 3) 交给 Runner,和 LLM 一起跑工具循环
r := &agent.Runner{Client: openai.New(key), Tools: tools}
resp, _ := r.Run(ctx, llm.Request{
    Model:    "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "帮我算 2+3"}},
})
fmt.Println(resp.Content)
```

- **`Tools(ctx, sess)`**:列举会话上全部工具并各自桥接;通常建会话后调一次。
- **`ToolFrom(sess, t)`**:桥接单个工具(需要挑选/改名/包装时用)。
- 入参 schema 取自 MCP 工具的 `InputSchema`;调用转发到 `sess.CallTool`,聚合文本内容块为结果。
  MCP 报错(`IsError`/传输错误)转成 Go error —— 交给 `Runner` 会作为错误文本喂回,让模型自愈。

## 边界

选连哪个 server、鉴权、给模型哪些工具、要不要人工审批都是 policy。本包只做
"列举 + 名称/入参透传 + 调用转发 + 文本结果聚合",不掺业务,也不内置任何工具。

> **版本**:本模块同时用到 `llm`(含 `llm/agent` 与工具调用)与 `mcp`,`go.mod` 里用
> `replace` 指向本地两模块联调;发布前去掉 `replace` 并把 `require` 指向已发布 tag。
