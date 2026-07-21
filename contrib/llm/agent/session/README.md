# llm/agent/session —— 会话记忆(持久化 + 滚动摘要)

给 [`llm/agent`](..) 的 `Runner` 加"记得上一轮":把多轮对话历史持久化,超长时滚动摘要。
`Manager` 是 `Runner` 之上的薄编排,纯标准库、零外部依赖。

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
    Store: session.NewMemoryStore(), // 生产可换 sqldb/redis 实现(实现 session.Store 即可)
    Summarizer: &session.Summarizer{ // 可选:超阈值滚动摘要
        Client: cli, Model: "gpt-4o-mini", MaxMessages: 20, KeepRecent: 6,
    },
}

// 每轮只传本轮新输入;Manager 负责拼历史/摘要、跑 Runner、回写、保存。
resp, _ := mgr.Run(ctx, "session-123", r, llm.Request{
    Model:    "gpt-4o",
    Messages: []llm.Message{{Role: llm.User, Content: "接着上次说"}},
})
```

- **`Store`**:`Load(id)`(不存在返回 `nil,nil`)/`Save`。内置并发安全的 `MemoryStore`;
  生产实现 sqldb/redis 版即可(接口一个)。
- **`Manager.Run(ctx, id, runner, req)`**:`req.Messages` 只放**本轮新输入**;返回后本轮
  user 输入与最终 assistant 回复已追加进会话。旧摘要作为系统背景注入下一轮。
- **`Summarizer`**:消息数超过 `MaxMessages` 时,把最早的一批折叠进 `Summary`,只留最近
  `KeepRecent` 条(用它自己的 `Client`/`Model` 生成摘要)。为 nil 则历史一直增长。

## 边界

存哪、何时摘要、保留多少条、摘要用哪个模型都是 policy(可配)。本包只做接口 + 内存实现 + 编排。
注:`Manager` 持久化的是 user/assistant 回合(对话历史),单次 `Run` 内部的工具往返是临时的,不入库。
