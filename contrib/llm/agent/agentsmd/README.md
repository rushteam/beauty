# llm/agent/agentsmd —— AGENTS.md 级联注入

从工作目录沿父目录向上收集 `AGENTS.md`,按**仓库根 → cwd**(通用 → 具体)拼进系统提示。
实现 `agent.ContextProvider`,可直接挂到 `Runner.ContextProvs`。

## 行为

1. 从 `Dir`(默认进程 cwd)开始向上查找 `AGENTS.md`
2. 默认遇到含 `.git` 的目录即停止(将该层纳入)
3. 也可设 `Root` 显式截断
4. 多段按「通用 → 具体」拼接后追加到 `req.System`

越靠近工作目录的约定越靠后,优先级更高。

## 用法

```go
import (
    "github.com/rushteam/beauty/contrib/llm/agent"
    "github.com/rushteam/beauty/contrib/llm/agent/agentsmd"
)

r := &agent.Runner{
    Client: client,
    ContextProvs: []agent.ContextProvider{
        agentsmd.New("/path/to/workdir"), // 或 &agentsmd.Provider{Dir: cwd, Root: repoRoot}
    },
}
```

与 Skills 一起用:

```go
ContextProvs: []agent.ContextProvider{
    agentsmd.New(cwd),
    sk.AsContextProvider(),
},
```
