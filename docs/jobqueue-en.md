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

### 1. Priority

Heap ordering based on `pkg/foundation/priority`. When multiple jobs are waiting, the one with the smallest value is consumed first:

```go
q.Submit(&jobqueue.Job{ID: "urgent", Priority: 0, ...})  // runs first
q.Submit(&jobqueue.Job{ID: "normal", Priority: 10, ...}) // runs later
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
