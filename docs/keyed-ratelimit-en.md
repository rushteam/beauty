# Grouped Rate Limiting (pkg/resilience/ratelimit — KeyedLimiter)

> 中文版: [keyed-ratelimit.md](keyed-ratelimit.md)

Maintains an independent Limiter instance per **group**; each group has its own token bucket/sliding window and does not interfere with the others.

## Use Cases

- **Multi-tenant rate limiting**: an independent QPS quota for each tenant
- **Per-API grouping**: independent rate limits for different endpoints
- **Per-user tiers**: different rates for VIP / regular users
- BullMQ Pro's **Group Rate Limit** semantics

## Usage

```go
// create an independent token bucket for each group (burst 100, 10/s)
kl := ratelimit.NewKeyedLimiter(func(group string) ratelimit.Limiter {
    return ratelimit.NewTokenBucket(100, 10)
})
defer kl.Stop()

// rate limit by key within a group
allowed, retryAfter := kl.Allow("tenant-A", "user-123")

// rate limit globally by group only
allowed, retryAfter = kl.AllowGroup("tenant-B")
```

## Configuration

```go
kl := ratelimit.NewKeyedLimiter(factory,
    ratelimit.WithKeyedMaxIdle(10*time.Minute),  // reclaim groups after idle timeout
    ratelimit.WithKeyedGcInterval(2*time.Minute), // gc scan interval
)
```

## API

| Method | Description |
|------|------|
| `Allow(group, key)` | Rate limit the key within the group |
| `AllowGroup(group)` | Rate limit globally by group (key fixed to empty) |
| `Groups()` | Number of currently active groups |
| `Stop()` | Stop gc and clean up all child limiters |

## Relationship with Existing Limiters

```
TokenBucket / SlidingWindow   — single limiter (isolated by key)
          │
          ├── Middleware()     — HTTP middleware
          │
          └── KeyedLimiter    — outer layer: adds another level of isolation by group
                                 each group internally is an independent Limiter
```

Groups idle longer than `WithKeyedMaxIdle` are reclaimed automatically (along with their child limiters' gc goroutines) to avoid memory leaks.
