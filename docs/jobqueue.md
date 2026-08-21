# 任务队列 (pkg/orchestration/jobqueue)

借鉴 BullMQ 架构的进程内任务队列,支持**优先级排序、进度上报、生命周期事件**。

## 与相邻包的区别

| 包 | 定位 |
|---|---|
| `scheduler` | FIFO 工作池(无优先级/进度/事件) |
| `delayqueue` | 到点触发回调(不关心执行状态) |
| `timerqueue` | 同 delayqueue,实现 beauty.Service |
| **`jobqueue`** | 完整 Job 生命周期:排队→执行→报告进度→完成/失败 |

## 快速上手

```go
q := jobqueue.New(
    jobqueue.WithWorkers(8),
    jobqueue.WithHookFunc(func(e jobqueue.Event) {
        slog.Info("job event", "type", e.Type, "job", e.Job.Name)
    }),
)
go q.Start(ctx) // 实现 beauty.Service,可直接 WithService(q) 挂入框架

// 投递任务
q.Submit(&jobqueue.Job{
    ID:       "order-123",
    Name:     "send-email",
    Priority: 5,          // 数值越小越优先(0 最高)
    Payload:  orderData,
    Timeout:  30 * time.Second,
    MaxRetries: 3,
    RetryDelay: time.Second,
    Fn: func(ctx context.Context, job *jobqueue.Job) error {
        jobqueue.ReportProgress(ctx, 50) // 进度上报
        if err := sendEmail(job.Payload); err != nil {
            return err // 触发重试
        }
        jobqueue.ReportProgress(ctx, 100)
        return nil
    },
})
```

## 核心特性

### 1. 优先级

基于 `pkg/foundation/priority` 堆排序。多个任务等待时,数值最小的先消费:

```go
q.Submit(&jobqueue.Job{ID: "urgent", Priority: 0, ...})  // 先执行
q.Submit(&jobqueue.Job{ID: "normal", Priority: 10, ...}) // 后执行
```

### 2. 进度上报

在 `Job.Fn` 内调用 `ReportProgress(ctx, percent)`,触发 `EventProgress` 钩子:

```go
Fn: func(ctx context.Context, job *jobqueue.Job) error {
    for i, item := range items {
        process(item)
        jobqueue.ReportProgress(ctx, float64(i+1)/float64(len(items))*100)
    }
    return nil
}
```

### 3. 生命周期事件

通过 `WithHook` 或 `WithHookFunc` 订阅事件,用于 metrics/日志/Dashboard:

| 事件 | 时机 |
|------|------|
| `EventSubmit` | 任务入队 |
| `EventStart` | 开始执行 |
| `EventProgress` | 进度更新 |
| `EventComplete` | 执行成功 |
| `EventFail` | 失败(含重试耗尽) |
| `EventRetry` | 即将重试 |

### 4. 延迟投递

```go
q.Submit(&jobqueue.Job{
    ID:    "reminder",
    Delay: 15 * time.Minute, // 15 分钟后才进入就绪队列
    Fn:    sendReminder,
})
```

### 5. Pause / Resume

```go
q.Pause()   // 暂停消费(不影响投递)
q.Resume()  // 恢复
q.Pending() // 查看待处理数
```

### 6. 取消

```go
q.Cancel("order-123") // 仅 Waiting/Delayed 状态可取消
```

## 分布式版本

进程内实现不持久化;需要跨进程 at-least-once 时使用 `contrib/redisqueue`(同一 API 风格)。
