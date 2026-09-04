# contrib/a2a —— A2A (Agent-to-Agent) 协议集成(独立模块)

桥接 [A2A 协议](https://github.com/a2aproject/a2a-go)与 beauty 的 `contrib/llm/agent` 体系:
本地 `StreamAgent` 可暴露为 A2A HTTP 服务,也可把远程 A2A agent 包装成 beauty `agent.Agent` 参与编排。
Task / Message / Artifact / TaskState 等概念由 a2a-go SDK 处理,本包只做类型转换与事件映射。

```bash
go get github.com/rushteam/beauty/contrib/a2a@latest
```

## 服务端:暴露 beauty agent

### 推荐:WithA2AServer(纳入 beauty 生命周期)

```go
import (
    "github.com/a2aproject/a2a-go/v2/a2a"
    a2ax "github.com/rushteam/beauty/contrib/a2a"
    "github.com/rushteam/beauty/pkg/service/webserver"
)

card := &a2a.AgentCard{Name: "reviewer", /* ... */}
app := beauty.New(
    a2ax.WithA2AServer(":5000", myAgent, a2ax.ServerConfig{AgentCard: card},
        webserver.WithServiceName("a2a-agent"),
    ),
)
app.Start(ctx)
```

自动享受 beauty 的优雅生命周期(启动→就绪→注册→排空→关闭)、OTel 追踪、中间件链及服务发现。

### 手动:RegisterRoutes(与其他路由共享 ServeMux)

```go
card := &a2a.AgentCard{Name: "reviewer", /* ... */}
mux := http.NewServeMux()
a2ax.RegisterRoutes(mux, myStreamAgent, a2ax.ServerConfig{AgentCard: card})
// JSON-RPC 在 "/" , AgentCard 在 /.well-known/agent-card.json
beauty.New(beauty.WithWebServer(":5000", mux)).Start(ctx)
```

`NewExecutor` 也可单独配合 a2a-go 的 `a2asrv.NewHandler` 使用。流式 token 映射为 Artifact 增量,
工具调用输出为 data artifact,暂停(HITL)映射为 `TaskStateInputRequired`。

## 客户端:消费远程 A2A agent

```go
import (
    "github.com/a2aproject/a2a-go/v2/a2aclient"
    "github.com/a2aproject/a2a-go/v2/a2asrv/agentcard"
    a2ax "github.com/rushteam/beauty/contrib/a2a"
)

card, _ := agentcard.DefaultResolver.Resolve(ctx, "http://remote:5000")
c, _ := a2aclient.NewFromCard(ctx, card)
remote := a2ax.NewAgent(c, a2ax.ClientConfig{Name: "remote-reviewer"})
outcome := remote.Run(ctx, llm.Request{Messages: msgs})
// Continue 用于 HITL 审批后继续同一 Task
```

## 边界

消息/Part 转换目前覆盖文本、图片 URL、工具调用 JSON;复杂 multimodal 策略由调用方扩展。
鉴权、AgentCard 内容、Task 持久化都是 policy;本包只做协议桥接。依赖 `a2a-go` 与 `contrib/llm`,
两者彼此零耦合。
