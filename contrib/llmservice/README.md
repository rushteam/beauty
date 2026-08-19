# contrib/llmservice —— Agent 即 beauty.Service(独立模块)

把 `contrib/llm/agent.Agent` 包装为 `beauty.Service`,接入 beauty 优雅停机顺序
(注销 → drain → 取消进行中 Run → flush checkpoint)。提供 worker 池、SSE HTTP、MQ 消费、
分布式锁与亲和路由,使 Agent 与 HTTP/gRPC/cron 共享同一生命周期。

```bash
go get github.com/rushteam/beauty/contrib/llmservice@latest
```

## 注册到 App

```go
import llmsvc "github.com/rushteam/beauty/contrib/llmservice"

svc := llmsvc.New("reviewer", runner,
    llmsvc.Workers(4),
    llmsvc.WithStore(checkpointStore),
    llmsvc.WithLocker(etcdLocker),
)

app := beauty.New(
    beauty.WithWebServer(":8080", mux),
    llmsvc.WithAgent("reviewer", runner, llmsvc.Workers(4)), // 或 beauty.WithService(svc)
)
svc.MountTo(mux) // POST /agents/reviewer/run|continue, GET /agents/reviewer/events
```

实现 `ReadyNotifier`:worker 就绪后才 signal ready,配合注册中心排空语义。

## MQ 异步消费

```go
consumer := mq.NewConsumer(broker, "agent-tasks")
consumer.Handle("agent.run.reviewer", llmsvc.MQHandler(svc))

// 发布侧
llmsvc.PublishTask(ctx, pub, "agent.run.reviewer", llmsvc.Task{
    Request: llm.Request{Messages: msgs},
})
```

`Task.RunID` 非空表示 Continue(HITL);`Resolutions` 携带审批结果。同一 `run_id` 可通过
`WithLocker` 保证多节点互斥续跑。

## 边界

Agent 逻辑、工具实现、checkpoint 存储选型都是 policy;本包负责 Service 封装与任务投递。
依赖 beauty core + `contrib/llm`。HTTP Handler 复用 `llm/agent/httpui`。
