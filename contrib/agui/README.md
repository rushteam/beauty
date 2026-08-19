# contrib/agui —— AG-UI SSE 流式协议(独立模块)

桥接 [AG-UI 协议](https://github.com/ag-ui-protocol/ag-ui)与 beauty 的 `contrib/llm/agent`:
POST 发送 `RunAgentInput` JSON,响应为 SSE 事件流(`RUN_STARTED` / `TEXT_MESSAGE_*` /
`TOOL_CALL_*` / `RUN_FINISHED` 等)。适合前端 Agent UI 与 Go 后端的标准化对接。

```bash
go get github.com/rushteam/beauty/contrib/agui@latest
```

## 服务端:暴露 StreamAgent

```go
import aguix "github.com/rushteam/beauty/contrib/agui"

h := aguix.NewHandler(myStreamAgent, aguix.HandlerConfig{AgentName: "assistant"})
http.Handle("/agent", h)
// POST /agent  Body: {"threadId":"...","messages":[{"role":"user","content":"hi"}]}
// Response: text/event-stream, data: {"type":"TEXT_MESSAGE_CONTENT","delta":"..."}
```

Handler 自动补全 `threadId` / `runId`,将 agent 流式事件(token / 思考 / 工具调用 / 步骤)
映射为 AG-UI 事件序列。

## 客户端:消费远程 AG-UI 服务

```go
remote := aguix.NewAgent("http://remote:8080/agent", aguix.ClientConfig{Name: "remote-ui"})
outcome := remote.Run(ctx, llm.Request{Messages: msgs})

// 或流式
for ev := range remote.RunStream(ctx, req) {
    switch ev.Type { /* agent.EventToken / EventFinal / ... */ }
}
```

`Continue` 通过追加用户消息模拟(AG-UI 无原生 continue 语义)。

## 边界

工具声明(`InputTool`)仅透传给 agent,不在本包执行。鉴权由外层 HTTP 中间件负责。
依赖 `contrib/llm`,不 import beauty 核心;单测覆盖 SSE 解析与事件映射。
