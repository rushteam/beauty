# statesync-interpolate — Actor + DeltaSync + 渲染插值组合演示

展示三大核心能力的组合:

| 层 | 包 | 职责 |
|---|---|---|
| **Actor Pipeline** | `pkg/game/match` | 单 goroutine 串行化,业务无锁 |
| **Delta Sync** | `pkg/game/replicate` | 只发脏数据的增量同步(AOI + DirtySet) |
| **Visual Delay Buffer** | `pkg/game/interpolate` | 客户端渲染延迟缓冲 + 帧间插值 |

## 原理

```
服务器(20Hz)                          客户端(60fps)
┌──────────┐   Subscribe/WebSocket    ┌──────────────────────┐
│ match    │ ─────────────────────→   │ interpolate.Buffer   │
│ .Match   │   每 50ms 一帧世界快照    │   ↓                  │
└──────────┘                          │ TimeLine.RenderTime()│
                                      │   ↓                  │
                                      │ Buffer.Bracket(t)    │
                                      │   ↓                  │
                                      │ InterpolateFrame()   │
                                      │   = 60fps 丝滑渲染   │
                                      └──────────────────────┘
```

客户端始终渲染"过去 100ms"的世界状态。收到服务器帧后存入 Buffer,渲染循环从 Buffer 找前后两帧做线性插值。代价是固定 100ms 延迟,换来完全吸收网络抖动。

## 运行

```bash
go run ./examples/statesync-interpolate
```

输出示例:

```
服务器: 20Hz tick | 客户端: 60fps render | 渲染延迟: 100ms

┌─────────────────────────┬───────────┬────────────────┬──────────────┐
│ 玩家                    │ 渲染帧数  │ 帧间平滑度(σ)  │ 结论         │
├─────────────────────────┼───────────┼────────────────┼──────────────┤
│ alice(稳定网络)          │     170   │       0.14     │ ✅ 丝滑       │
│ bob(普通网络)            │     170   │       0.13     │ ✅ 丝滑       │
│ carol(高抖动)            │     170   │       0.13     │ ✅ 丝滑       │
└─────────────────────────┴───────────┴────────────────┴──────────────┘
```

即使 carol 的网络抖动高达 50ms,通过 100ms 渲染缓冲仍然完全平滑。
