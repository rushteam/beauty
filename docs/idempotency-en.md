# Idempotency Primitive (pkg/store/idempotency)

> 中文版: [idempotency.md](idempotency.md)

Repeated requests with the same **idempotency key** should produce a side effect only once; network retries, message redelivery, and client double-clicks all need this layer of protection.

## Two APIs

| API | Semantics | Use for |
|-----|------|------|
| `Do(key, fn)` | In-memory deduplication + **singleflight** (concurrent calls with the same key block and wait for the first result) | Pure computation, deterministic operations |
| `Acquire` / `Commit` / `Release` | **SETNX-first**: claim the key first, then execute; release on failure; write the result on success | Operations with external side effects (DB/RPC/sending messages) |

```go
store := idempotency.New[OrderResult](idempotency.WithTTL(10*time.Minute))

// wrapper style
out, err, reused := store.Do("order-123", func() (OrderResult, error) {
    return createOrder(ctx)
})

// guard style (recommended for side-effecting operations)
guard, err := store.Acquire(ctx, "pay-456")
if err == idempotency.ErrConflict { /* another instance is processing it */ }
if err != nil { return err }
result, err := chargeWallet(ctx)
if err != nil { guard.Release(); return err }
return guard.Commit(result)
```

## Working with store mode (Redis)

`WithStore(redisStore)` makes results reusable **across instances**:

- The caller that wins the SETNX executes `fn` and calls `Commit`
- Callers that lose read the existing result; if it has not been written yet, each may execute once on its own (**at-least-once**)
- For strict global single-flight, add a distributed lock inside `fn`, or rely on business-level idempotency

`WithCacheErrors` only takes effect in **in-memory mode**; store mode persists successful results only.

## Choosing

- "Execute only once per key and reuse the result" → `Do` or `Acquire/Commit`
- "Serialize per key, but run every time" → `pkg/foundation/keyedmutex`
- "Count quota within a window" → `pkg/resilience/counter` / `ratelimit`
