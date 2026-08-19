# 幂等原语 (pkg/store/idempotency)

同一 **幂等键** 的重复请求只应产生一次副作用;网络重试、消息重投、客户端双点都需要这层保护。

## 两套 API

| API | 语义 | 适用 |
|-----|------|------|
| `Do(key, fn)` | 内存去重 + **singleflight**(并发同 key 阻塞等首次结果) | 纯计算、确定性操作 |
| `Acquire` / `Commit` / `Release` | **SETNX-first**:先占位再执行,失败释放;成功写入结果 | 有外部副作用(DB/RPC/发消息) |

```go
store := idempotency.New[OrderResult](idempotency.WithTTL(10*time.Minute))

// 包裹式
out, err, reused := store.Do("order-123", func() (OrderResult, error) {
    return createOrder(ctx)
})

// 守卫式(副作用操作推荐)
guard, err := store.Acquire(ctx, "pay-456")
if err == idempotency.ErrConflict { /* 其他实例正在处理 */ }
if err != nil { return err }
result, err := chargeWallet(ctx)
if err != nil { guard.Release(); return err }
return guard.Commit(result)
```

## 与 store 模式(Redis) 配合

`WithStore(redisStore)` 使结果**跨实例**复用:

- 抢到 SETNX 的执行 `fn` 并 `Commit`
- 未抢到的读已有结果;若尚未写完可能各自执行一次(**at-least-once**)
- 严格全局单飞请在 `fn` 内加分布式锁,或接受业务幂等

`WithCacheErrors` 仅**内存模式**生效;store 模式只持久化成功结果。

## 选型

- 「同 key 只执行一次并复用结果」→ `Do` 或 `Acquire/Commit`
- 「同 key 串行但每次都要跑」→ `pkg/foundation/keyedmutex`
- 「窗口内次数配额」→ `pkg/resilience/counter` / `ratelimit`
