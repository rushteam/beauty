# Redis 分布式任务队列 (contrib/redisqueue)

基于 Redis 的分布式 Job Queue,实现 BullMQ 风格的**优先级 + 延迟 + 重试 + 事件**。

## 与进程内 jobqueue 的区别

| | `pkg/orchestration/jobqueue` | `contrib/redisqueue` |
|---|---|---|
| 存储 | 进程内存 | Redis |
| 持久化 | 否(进程崩溃丢失) | 是 |
| 分布式 | 否(单进程) | 是(多 worker 水平扩展) |
| 投递保证 | at-most-once | **at-least-once**(可见性超时) |
| 适用 | 开发/测试/轻量场景 | 生产分布式部署 |

## 安装

```bash
go get github.com/rushteam/beauty/contrib/redisqueue@latest
```

## 快速上手

```go
import (
    "github.com/redis/go-redis/v9"
    "github.com/rushteam/beauty/contrib/redisqueue"
)

rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
q := redisqueue.New(rdb, "email-tasks",
    redisqueue.WithVisibilityTime(60*time.Second),
    redisqueue.WithHook(func(e redisqueue.Event) {
        metrics.JobEvent(e.Type, e.Job.Name)
    }),
)

// 投递
q.Submit(ctx, &redisqueue.Job{
    ID:         "email-001",
    Name:       "send-welcome",
    Payload:    jsonBytes,
    Priority:   5,
    MaxRetries: 3,
    RetryDelay: 5 * time.Second,
    Timeout:    30 * time.Second,
})

// 消费(可多进程水平扩展)
q.StartWorker(ctx, func(ctx context.Context, job *redisqueue.Job) error {
    var payload EmailPayload
    json.Unmarshal(job.Payload, &payload)
    return mailer.Send(ctx, payload)
})
```

## Redis 数据结构

```
{prefix}:{queue}:waiting   — Sorted Set (score = priority)
{prefix}:{queue}:delayed   — Sorted Set (score = 到期 unix ms)
{prefix}:{queue}:active    — Set (正在处理的 job ID)
{prefix}:{queue}:completed — Set
{prefix}:{queue}:failed    — Set
{prefix}:{queue}:job:{id}  — String (JSON 序列化的 Job)
{prefix}:{queue}:lock:{id} — String (可见性超时 key, TTL = VisibilityTime)
```

## 核心机制

### 优先级

Sorted Set 的 score 即 priority,`ZPOPMIN` 取出最小分数(最高优先级):

```go
q.Submit(ctx, &redisqueue.Job{ID: "urgent", Priority: 0})  // 先消费
q.Submit(ctx, &redisqueue.Job{ID: "normal", Priority: 10}) // 后消费
```

### 延迟任务

投递时指定 `Delay`,先进入 `delayed` Sorted Set(score=到期时间戳);后台 scheduler
定期扫描到期任务,转入 `waiting`。

### at-least-once 与 Stalled 检测

- Worker 取出任务时设置 `lock:{id}` (TTL = VisibilityTime)
- 处理完成后删除 lock
- 后台 stalledChecker 定期扫描 `active` 集合:lock 已过期 → worker 崩溃,自动重新入队

### 重试

失败时按指数退避放入 `delayed`,到期后重新进入 `waiting` 被消费:

```
第 1 次重试: delay = RetryDelay * 1
第 2 次重试: delay = RetryDelay * 2
第 3 次重试: delay = RetryDelay * 4
...
```

### 进度上报

```go
q.ReportProgress(ctx, jobID, 75.0)
```

## 配置

| 选项 | 默认 | 说明 |
|------|------|------|
| `WithPrefix` | `"bq"` | Redis key 前缀 |
| `WithPollInterval` | `1s` | 空闲轮询间隔 |
| `WithVisibilityTime` | `30s` | 可见性超时(崩溃后多久重投) |
| `WithDelayResolution` | `500ms` | 延迟任务检查间隔 |
| `WithHook` | nil | 事件回调 |

## 运维

### 查询任务状态

```go
job, err := q.GetJob(ctx, "email-001")
fmt.Println(job.State, job.Attempts, job.Progress)
```

### 清理历史

```go
removed, _ := q.Clean(ctx, redisqueue.StateCompleted, 1000) // 保留最近 1000 个
removed, _ = q.Clean(ctx, redisqueue.StateFailed, 100)
```

### 监控

通过 `WithHook` 接入 Prometheus/OTel:

```go
redisqueue.WithHook(func(e redisqueue.Event) {
    jobCounter.WithLabelValues(string(e.Type), e.Job.Name).Inc()
    if e.Type == redisqueue.EventComplete {
        jobDuration.Observe(float64(e.Job.DoneAt - e.Job.StartedAt) / 1000)
    }
})
```
