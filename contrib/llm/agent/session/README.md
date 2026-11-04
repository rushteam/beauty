# llm/agent/session —— 会话记忆(持久化 + 滚动摘要)

给 [`llm/agent`](..) 的 `Runner` 加"记得上一轮":把多轮对话历史持久化,超长时滚动摘要。
`Manager` 是 `Runner` 之上的薄编排,纯标准库、零外部依赖。每次跑完会记下一条 run
(子 run 挂在顶层下面)并把 token 用量累加到会话上,不从被摘要截断的消息回推。

```go
page, _ := sess.RunsPage(session.RunQuery{Limit: 20})          // 最新一页
older, _ := sess.RunsPage(session.RunQuery{Limit: 20, Before: &page.Oldest})
```

## 用法

```go
import (
    "github.com/rushteam/beauty/contrib/llm"
    "github.com/rushteam/beauty/contrib/llm/agent"
    "github.com/rushteam/beauty/contrib/llm/agent/session"
    "github.com/rushteam/beauty/contrib/llm/openai"
)

cli := openai.New(key)
r := &agent.Runner{Client: cli /*, Tools: ... */}

mgr := &session.Manager{
    Store: session.NewMemoryStore(), // 开发/测试/单副本；生产改用 contrib/llmsession
    Summarizer: &session.Summarizer{ // 可选:超阈值滚动摘要
        Client: cli, Model: "gpt-4o-mini", MaxMessages: 20, KeepRecent: 6,
        MaxInputTokens: 3000, // 可选:摘要请求只带放得下的完整前缀
    },
}

// 每轮只传本轮新输入;Manager 负责拼历史/摘要、跑 Runner、回写、保存。
resp, _ := mgr.Run(ctx, "session-123", r, llm.Request{
    Model:    "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "接着上次说"}},
})

// 或事件流(Final 时自动落盘)
for ev := range mgr.RunStream(ctx, "session-123", r, req) {
    // token / step / tool_* / final / error
}
```

- **`Store`**:`Load(id)`(不存在返回 `nil,nil`)/`Save`。内置并发安全的 `MemoryStore`,以及
  `FileStore`(每会话一个 JSON 文件,零依赖持久化);生产 SQLite/Redis 见
  [`contrib/llmsession`](../../../llmsession)。
- **`Manager.Run` / `RunStream`**:`req.Messages` 只放**本轮新输入**;`RunStream` 转发
  `Runner` 事件,仅在 `EventFinal` 时持久化。
- **`Summarizer`**:消息数超过 `MaxMessages` 时,把最早的一批折叠进 `Summary`,只留最近
  `KeepRecent` 条(用它自己的 `Client`/`Model` 生成摘要)。为 nil 则历史一直增长。

## 边界

存哪、何时摘要、保留多少条、摘要用哪个模型都是 policy(可配)。本包只做接口 + MemoryStore + FileStore + 编排。
注:`Manager` 持久化的是 user/assistant 回合(对话历史),单次 `Run` 内部的工具往返是临时的,不入库。

## 生产建议

`MemoryStore` 在重启后丢失，不能跨副本共享；`FileStore` 适合单进程附属卷，不提供跨进程事务或写入协调。
多副本使用 `contrib/llmsession` 的 Redis 或 SQL Store，并以业务 session ID 路由。会话写入是 Store 的一致性边界：
若同一 session 可并发请求，Store 实现应使用版本/CAS 或业务侧串行化，避免最后一次写入覆盖另一轮。
