# 分组限流 (pkg/resilience/ratelimit — KeyedLimiter)

按 **group** 维护独立的 Limiter 实例,每组拥有自己的令牌桶/滑动窗口,互不干扰。

## 场景

- **多租户限流**:每个租户独立 QPS 额度
- **按 API 分组**:不同接口独立限流
- **按用户分级**:VIP / 普通用户不同速率
- BullMQ Pro 的 **Group Rate Limit** 语义

## 用法

```go
// 为每个 group 创建独立的令牌桶(100 突发,10/s)
kl := ratelimit.NewKeyedLimiter(func(group string) ratelimit.Limiter {
    return ratelimit.NewTokenBucket(100, 10)
})
defer kl.Stop()

// group 内按 key 限流
allowed, retryAfter := kl.Allow("tenant-A", "user-123")

// 仅按 group 全局限流
allowed, retryAfter = kl.AllowGroup("tenant-B")
```

## 配置

```go
kl := ratelimit.NewKeyedLimiter(factory,
    ratelimit.WithKeyedMaxIdle(10*time.Minute),  // group 空闲超时回收
    ratelimit.WithKeyedGcInterval(2*time.Minute), // gc 扫描间隔
)
```

## API

| 方法 | 说明 |
|------|------|
| `Allow(group, key)` | 对 group 下的 key 限流 |
| `AllowGroup(group)` | 按 group 全局限流(key 固定为空) |
| `Groups()` | 当前活跃 group 数 |
| `Stop()` | 停止 gc,清理所有子 limiter |

## 与已有 Limiter 的关系

```
TokenBucket / SlidingWindow   — 单个限流器(按 key 隔离)
          │
          ├── Middleware()     — HTTP 中间件
          │
          └── KeyedLimiter    — 外层:按 group 再隔离一层
                                 每个 group 内部是一个独立的 Limiter
```

idle 超过 `WithKeyedMaxIdle` 的 group 自动回收(及其子 limiter 的 gc goroutine),避免内存泄漏。
