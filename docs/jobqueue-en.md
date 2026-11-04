# Job Queue (pkg/orchestration/jobqueue)

> 中文版: [jobqueue.md](jobqueue.md)

An in-process job queue modeled on the BullMQ architecture, supporting **priority ordering, progress reporting, and lifecycle events**.

## How it differs from neighboring packages

| Package | Role |
|---|---|
| `scheduler` | FIFO worker pool (no priority/progress/events) |
| `delayqueue` | Fires a callback when due (does not track execution state) |
| `timerqueue` | Same as delayqueue, implements beauty.Service |
| **`jobqueue`** | Full job lifecycle: queued → running → progress reporting → completed/failed |

## Quick start

```go
q := jobqueue.New(
    jobqueue.WithWorkers(8),
    jobqueue.WithHookFunc(func(e jobqueue.Event) {
        slog.Info("job event", "type", e.Type, "job", e.Job.Name)
    }),
)
go q.Start(ctx) // implements beauty.Service; can be mounted directly via WithService(q)

// submit a job
q.Submit(&jobqueue.Job{
    ID:       "order-123",
    Name:     "send-email",
    Priority: 5,          // smaller value = higher priority (0 is highest)
    Payload:  orderData,
    Timeout:  30 * time.Second,
    MaxRetries: 3,
    RetryDelay: time.Second,
    Fn: func(ctx context.Context, job *jobqueue.Job) error {
        jobqueue.ReportProgress(ctx, 50) // report progress
        if err := sendEmail(job.Payload); err != nil {
            return err // triggers a retry
        }
        jobqueue.ReportProgress(ctx, 100)
        return nil
    },
})
```

## Core features

### 1. Priority and ratio

By default each priority has its own lane: a smaller value runs its whole lane before the next one. Inside a lane, jobs run in arrival order.

```go
q.Submit(&jobqueue.Job{ID: "a", Priority: 5, ...})
q.Submit(&jobqueue.Job{ID: "b", Priority: 5, ...})
q.Submit(&jobqueue.Job{ID: "vip", Priority: 0, ...})
// order: vip, a, b
```

`WithRatio` interleaves lanes instead of letting the higher priority drain first. The example releases one VIP after every five normal jobs. An empty lane is skipped. Share order is the order inside one cycle. Priorities not listed get one release per cycle, after the listed shares, smaller values first.

```go
q := jobqueue.New(jobqueue.WithRatio(
    jobqueue.Share{Priority: 10, Count: 5}, // normal
    jobqueue.Share{Priority: 0, Count: 1},  // VIP
))
```

### 2. Progress reporting

Call `ReportProgress(ctx, percent)` inside `Job.Fn` to fire the `EventProgress` hook:

```go
Fn: func(ctx context.Context, job *jobqueue.Job) error {
    for i, item := range items {
        process(item)
        jobqueue.ReportProgress(ctx, float64(i+1)/float64(len(items))*100)
    }
    return nil
}
```

### 3. Lifecycle events

Subscribe to events via `WithHook` or `WithHookFunc`, for metrics/logging/dashboards:

| Event | When |
|------|------|
| `EventSubmit` | Job enqueued |
| `EventStart` | Execution starts |
| `EventProgress` | Progress updated |
| `EventComplete` | Execution succeeded |
| `EventFail` | Failed (including retries exhausted) |
| `EventRetry` | About to retry |

### 4. Delayed delivery

```go
q.Submit(&jobqueue.Job{
    ID:    "reminder",
    Delay: 15 * time.Minute, // enters the ready queue only after 15 minutes
    Fn:    sendReminder,
})
```

### 5. Pause / Resume

```go
q.Pause()   // pause consumption (submission is unaffected)
q.Resume()  // resume
q.Pending() // number of pending jobs
```

### 6. Cancellation

```go
q.Cancel("order-123") // only jobs in Waiting/Delayed state can be cancelled
```

## Distributed version

The in-process implementation does not persist anything; when you need cross-process at-least-once delivery, use `contrib/redisqueue` (same API style).
