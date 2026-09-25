# Redis Distributed Task Queue (contrib/redisqueue)

> 中文版: [redisqueue.md](redisqueue.md)

A Redis-based distributed Job Queue implementing BullMQ-style **priority + delay + retry + events**.

## Differences from the In-Process jobqueue

| | `pkg/orchestration/jobqueue` | `contrib/redisqueue` |
|---|---|---|
| Storage | Process memory | Redis |
| Persistence | No (lost on process crash) | Yes |
| Distributed | No (single process) | Yes (horizontal scaling with multiple workers) |
| Delivery guarantee | at-most-once | **at-least-once** (visibility timeout) |
| Suitable for | Development/testing/lightweight scenarios | Production distributed deployments |

## Installation

```bash
go get github.com/rushteam/beauty/contrib/redisqueue@latest
```

## Quick Start

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

// submit
q.Submit(ctx, &redisqueue.Job{
    ID:         "email-001",
    Name:       "send-welcome",
    Payload:    jsonBytes,
    Priority:   5,
    MaxRetries: 3,
    RetryDelay: 5 * time.Second,
    Timeout:    30 * time.Second,
})

// consume (can scale horizontally across multiple processes)
q.StartWorker(ctx, func(ctx context.Context, job *redisqueue.Job) error {
    var payload EmailPayload
    json.Unmarshal(job.Payload, &payload)
    return mailer.Send(ctx, payload)
})
```

## Redis Data Structures

```
{prefix}:{queue}:waiting   — Sorted Set (score = priority)
{prefix}:{queue}:delayed   — Sorted Set (score = due time in unix ms)
{prefix}:{queue}:active    — Set (IDs of jobs being processed)
{prefix}:{queue}:completed — Set
{prefix}:{queue}:failed    — Set
{prefix}:{queue}:job:{id}  — String (JSON-serialized Job)
{prefix}:{queue}:lock:{id} — String (visibility timeout key, TTL = VisibilityTime)
```

## Core Mechanisms

### Priority

The Sorted Set score is the priority; `ZPOPMIN` pops the lowest score (highest priority):

```go
q.Submit(ctx, &redisqueue.Job{ID: "urgent", Priority: 0})  // consumed first
q.Submit(ctx, &redisqueue.Job{ID: "normal", Priority: 10}) // consumed later
```

### Delayed Jobs

Specify `Delay` when submitting, and the job first enters the `delayed` Sorted Set (score = due timestamp); a background scheduler
periodically scans for due jobs and moves them into `waiting`.

### at-least-once and Stalled Detection

- When a worker picks up a job, it sets `lock:{id}` (TTL = VisibilityTime)
- The lock is deleted after processing completes
- A background stalledChecker periodically scans the `active` set: if the lock has expired → the worker crashed, and the job is automatically re-enqueued

### Retry

On failure, the job is placed into `delayed` with exponential backoff, and after it becomes due it re-enters `waiting` to be consumed:

```
Retry 1: delay = RetryDelay * 1
Retry 2: delay = RetryDelay * 2
Retry 3: delay = RetryDelay * 4
...
```

### Progress Reporting

```go
q.ReportProgress(ctx, jobID, 75.0)
```

## Configuration

| Option | Default | Description |
|------|------|------|
| `WithPrefix` | `"bq"` | Redis key prefix |
| `WithPollInterval` | `1s` | Idle polling interval |
| `WithVisibilityTime` | `30s` | Visibility timeout (how long after a crash before redelivery) |
| `WithDelayResolution` | `500ms` | Delayed job check interval |
| `WithHook` | nil | Event callback |

## Operations

### Querying Job Status

```go
job, err := q.GetJob(ctx, "email-001")
fmt.Println(job.State, job.Attempts, job.Progress)
```

### Cleaning Up History

```go
removed, _ := q.Clean(ctx, redisqueue.StateCompleted, 1000) // keep the most recent 1000
removed, _ = q.Clean(ctx, redisqueue.StateFailed, 100)
```

### Monitoring

Integrate with Prometheus/OTel via `WithHook`:

```go
redisqueue.WithHook(func(e redisqueue.Event) {
    jobCounter.WithLabelValues(string(e.Type), e.Job.Name).Inc()
    if e.Type == redisqueue.EventComplete {
        jobDuration.Observe(float64(e.Job.DoneAt - e.Job.StartedAt) / 1000)
    }
})
```
