# 实时服务组件库（pkg/ · pkg/domain/）

beauty 在 `pkg/ws`（WebSocket 薄封装）和 `pkg/sse`（SSE 封装）之上,提供了一组
**可独立组合**的实时服务原语,覆盖长连接会话、在线状态、消息路由、匹配组队、
排行榜缓存、任务调度、虚拟账户、操作审计、离线通知、周期榜单、临时小队、
版本化存储、社交图谱十三类典型场景。它们均借鉴 [Nakama](https://github.com/heroiclabs/nakama)
游戏服务器的工程模型,落地为 beauty 风格(泛型 + 函数式 Option + 中文 doc + 纯标准库)。

包按"通用 vs 业务"分两个命名空间:

- **`pkg/`** —— 通用实时原语(不预设业务语义):会话、在场、路由、匹配、排名、调度、审计。
  这些是"频道/路由/状态机/排名"级别的工具,不绑定具体业务实体。
- **`pkg/domain/`** —— 业务实体(预设了业务模型):账户、通知、派对、锦标赛、存储、关系。
  这些包带"业务实体"语义(货币/通知/小队/赛季榜/存档/社交),归拢到 `domain`
  便于识别与隔离,import 路径统一 `pkg/domain/<name>`。

各包各司其职,无强耦合:你可以只用 `session` 做一个 echo 房间,也可以把
`presence` + `router` + `session` 串起来做一个 IM 频道,用 `match` +
`matchmaker` 做权威对战大厅,再用 `domain/wallet` + `domain/notification` + `audit`
补齐账户、通知与合规。本文给出每个包的速查与组合范式。

## 组件全景

### 通用原语(pkg/)

| 包 | 一句话 | 典型场景 | demo 端口 |
|----|--------|----------|-----------|
| `pkg/match` | 有状态实时会话原语(actor 模型) | 游戏房间 / 权威对战 / 协作编辑 | 8181 |
| `pkg/ws/session` | WebSocket 有状态会话高阶封装 | 长连接业务 / IM 单聊 | 8282 |
| `pkg/presence` | 在线状态双索引 + 事件总线 | 频道成员 / 在线广播 / 候选池 | 8283 |
| `pkg/router` | 多语义消息路由 + 攒批 | 群发 / 定点投递 / 批量下发 | 8284 |
| `pkg/leaderboard` | 排行榜内存排名缓存(堆排序) | "我的名次" / TopN 高频读 | 8285 |
| `pkg/scheduler` | 工作池 + 运行时 Pause/Resume | 发奖 / 批量通知 / 过期清理 | 8286 |
| `pkg/matchmaker` | 基于属性匹配的组队 | PVP 组队 / 匹配大厅 | 8287 |
| `pkg/audit` | 操作审计(仅记成功 + 异步落盘) | 合规 / 运维审计 | 8289 |

### 业务实体(pkg/domain/)

| 包 | 一句话 | 典型场景 | demo 端口 |
|----|--------|----------|-----------|
| `pkg/domain/wallet` | 不可变账本 + 余额派生(差值更新) | 虚拟货币 / 积分 / 库存 | 8288 |
| `pkg/domain/notification` | 持久/瞬时二分 + 离线拉取 | 离线消息 / 系统通知 | 8290 |
| `pkg/domain/tournament` | 锦标赛(leaderboard + cron 重置) | 赛季榜 / 每日挑战 | 8291 |
| `pkg/domain/party` | 无权威小队(Leader + 加入审核) | 好友开黑 / 临时小队 | 8292 |
| `pkg/domain/storage` | 版本化 KV + OCC 乐观锁 | 游戏存档 / 用户配置 | 8293 |
| `pkg/domain/relationship` | 社交图谱(二部有向图 + 状态编码) | 好友 / 关注 / 拉黑 / 群组 | 8294 |

> demo 源码均在 `examples/<pkg>/main.go`,单文件、约 50 行,可直接 `go run`。

## 组合范式

实时业务的关键在于"连接 ↔ 会话 ↔ 在场 ↔ 路由"四层。各包只解决其中一层,
组合方式如下:

```
        ┌─────────────── WebSocket / gRPC 长连接 ───────────────┐
        │                                                        │
   pkg/ws/session  ──(Handler.OnOpen/OnMessage/OnClose)──►  业务层
        │                                                        │
        │  Track/Untrack                  Send/QueueDeferred
        ▼                                ▼
   pkg/presence  ◄────── Lookup ──────  pkg/router
   (会话↔流 双索引)                    (按 presence ID / 流 / 全员 路由)

   pkg/match          pkg/matchmaker         pkg/leaderboard     pkg/scheduler
   (有状态房间)        (组队匹配)             (排名缓存)          (后台工作池)
        │                   │                      │                   │
        └─── Subscribe ───► 业务层 ◄── Match 回调 ─┘ ── Insert ───────┘

   ┌─── 横切支线(pkg/domain/ 业务实体)──────────────────────────────┐
   │  domain/wallet      domain/notification      audit              │
   │  (差值账本)           (持久/瞬时 + 离线)        (仅记成功)          │
   │      ▲                    ▲ liveSink              ▲ wrap(HTTP)     │
   │      │                    │                        │               │
   │      └── 扣账/发奖 ◄── 业务层 ──► 通知离线留存 ──► 成功操作落盘    │
   │                                                                    │
   │  domain/tournament (leaderboard+cron)  domain/party (无权威小队)   │
   │  domain/storage (版本化 KV+OCC)  domain/relationship (社交图谱)    │
   └────────────────────────────────────────────────────────────────────┘
```

**最小组合:有状态房间**——`session` 接入 `match`,`match.Tick` 产出经
`session.Send` 广播(见 `examples/match`)。

**典型组合:IM 频道**——`session` 维护连接,`presence` 登记在场,`router`
按流群发。客户端 `/join` 时 `presence.Track` + 注册 `router.Sink`,
`/say` 时 `router.SendToStream`(见 `examples/router` + `examples/presence`)。

**账户支线:发奖 + 通知 + 审计**——比赛结束 `wallet.Apply` 给队员发奖金
(原子防超扣),`notification.Send` 推送"奖金到账"(离线则留存),`audit`
中间件全程记录"管理员发奖"操作(见 `examples/wallet` + `notification` + `audit`)。

**周期榜:锦标赛**——`tournament.New("daily", desc, "0 0 * * *")` 每日 0 点
滚动新一周期,`Insert/TopN` 自动落到当前周期的 `leaderboard.RankCache`
(见 `examples/tournament`)。

**完整组合:匹配大厅**——`matchmaker.Add` 入队,匹配回调里 `presence.Track`
把队员加入同一流并 `match.Start` 开房,房间产出经 `router.SendToStream` 下发。

## 速查:pkg/match（有状态实时会话）

每个会话由独立 goroutine 驱动,固定帧率 tick,输入/成员/信号经 channel
串行消费,状态封装在 goroutine 内无需锁。参考 Nakama `match_handler.go`。

```go
m := match.New[GameState, Input, Msg](myHandler, nil,
    match.WithTickRate(20),          // Hz
    match.WithInputQueue(256),
    match.WithMaxIdleSec(60),
)
m.Start(ctx)
m.QueueInput(in)                     // 非阻塞,队列满丢弃
out, cancel := m.Subscribe(ctx)      // 订阅 Tick 产出
m.Stop(); m.Wait()                   // 优雅停止
```

业务实现 `Handler.Init/Tick`。背压:`QueueInput` 满则丢弃+告警,call 队列满则
视为过载停止。详见 `examples/match`。

## 速查:pkg/ws/session（WebSocket 会话封装）

在 `pkg/ws` 薄封装之上补齐生产级能力:双 goroutine 读写分离、周期 Ping 心跳、
关闭握手、写超时保护。参考 Nakama `session_ws.go`。

```go
mux.Handle("/ws", ws.Handler(session.Accept(&myHandler{},
    session.WithPingPeriod(30*time.Second),
    session.WithPingTimeout(5*time.Second),
), ws.WithInsecureSkipVerify()))
```

业务实现 `Handler.OnOpen/OnMessage/OnClose`,用 `s.Send/SendText/SendJSON` 投递写。
队列满自动关闭慢客户端。详见 `examples/session`。

## 速查:pkg/presence（在线状态双索引）

维护"谁在哪个流"的双向索引:按流查成员(广播用)、按会话查所在流(下线清理用),
双向均 O(1)。附 join/leave 事件总线。参考 Nakama `tracker.go`。

```go
tr := presence.New(func(stream presence.Stream, joins, leaves []*presence.Presence) {
    // 事件回调
}, 256)
tr.Track(sid, stream, presence.Meta{UserID: uid})
members := tr.ListByStream(stream, false)
tr.UntrackAll(sid)                    // 会话下线一键清理
```

并发安全。事件队列满丢弃(非阻塞)。详见 `examples/presence`。

## 速查:pkg/router（多语义消息路由）

`Broadcaster` 的增强版:按 presence ID 定点投递、按流群发、攒批下发。
参考 Nakama `message_router.go`。

```go
rtr := router.New(registry, tr)       // registry: sessionID→Sink,tr: presence.Tracker
rtr.SendToPresenceIDs(ids, msg, true)
n := rtr.SendToStream(stream, msg, false)  // 借助 presence 查成员
rtr.QueueDeferred(sids, msg); rtr.FlushDeferred()  // 攒批
rtr.SendToAll(msg)
```

`FlushDeferred` 按 session 批量下发,减少 Lookup。详见 `examples/router`。

## 速查:pkg/leaderboard（排行榜排名缓存）

用堆排序维护每个榜的有序结构,O(log N) 查"我的名次"、TopN、按名次取记录,
黑名单可排除写频繁的榜。参考 Nakama `leaderboard_rank_cache.go`。

```go
rc := leaderboard.New()
rc.Fill("score", 0, leaderboard.SortDescending, records, true)
rank := rc.Get("score", 0, userID)               // 查名次
top := rc.TopN("score", 0, 10)
newRank := rc.Insert("score", 0, leaderboard.SortDescending, rec, true)
rc.Delete("score", 0, userID)
```

并发安全。`Fill` 幂等(可重复加载)。详见 `examples/leaderboard`。

## 速查:pkg/scheduler（工作池 + Pause/Resume）

N 个 worker 并发消费队列,支持运行时 Pause/Resume 与优雅停止,worker panic 自动恢复。
与 `pkg/service/cron`(按表达式定时)互补——本包按事件 Submit。参考 Nakama
`leaderboard_scheduler.go`。

```go
s := scheduler.New(
    scheduler.WithWorkers(3),
    scheduler.WithQueueSize(100),
    scheduler.WithErrorHandler(handler),
)
s.Start(ctx)
s.Submit(&scheduler.Task{Name: "work", Fn: fn})
s.Pause(); s.Resume()                // 运行时控制
s.Stop(); s.Wait()                   // 优雅停止
```

`WithWorkers(0)` 允许纯队列模式(只排队不消费)。详见 `examples/scheduler`。

## 速查:pkg/matchmaker（属性组队匹配）

玩家带 string+numeric 属性注册 ticket,匹配器按"桶(region+mode)+ skill
排序贪心"凑队,凑齐回调。纯标准库实现,适合单机万级 ticket。
参考 Nakama `matchmaker.go`(Nakama 用 Bluge,本包轻量化)。

```go
m := matchmaker.New(onMatch, matchmaker.WithTickInterval(500*time.Millisecond), matchmaker.WithMaxWaitSec(15))
m.Start(ctx)
m.Add(matchmaker.Ticket{
    Presence:  matchmaker.Presence{UserID: uid, SessionID: sid},
    Properties: matchmaker.Properties{String: map[string]string{"region": "eu"}, Numeric: map[string]float64{"skill": 1000}},
    MinCount: 2, MaxCount: 3,
}, "5v5", "eu|ranked")
m.Remove(ticketID); m.Count()
```

超时放宽桶约束(`maxWaitSec`),避免长等待。详见 `examples/matchmaker`。

## 速查:pkg/domain/wallet（不可变账本 + 余额派生）

双模型:当前余额(快读)+ 只追加账本(可审计/回溯)。changeset 差值更新,
`<0` 即超扣,原子回滚。参考 Nakama `core_wallet.go`。

```go
w := wallet.New()
bal, l, err := w.Apply("u1", wallet.WalletMap{"gold": 100}, "init", now)
// 扣账:差值为负,余额不足即 ErrInsufficientBalance(回滚,账本不追加)
w.Apply("u1", wallet.WalletMap{"gold": -50}, "spend", now)
w.Balance("u1")      // {gold:50}
w.Ledgers("u1")      // 全量账本
w.LedgerByID("u1", l.ID)
w.SetBalance("u1", WalletMap{"gold": 999}) // 启动时从 DB 恢复,不产账本
```

并发安全。详见 `examples/wallet`。

## 速查:pkg/audit（操作审计,仅记成功）

结构化记录"谁对什么资源做了什么",仅记 `err==nil` 且状态码 < 500 的成功操作
(失败走 logger),异步落盘不阻塞业务。参考 Nakama `console_audit.go`。

```go
sink := audit.SinkFunc(func(ctx context.Context, e audit.Entry) error {
    // 写 DB / 文件
    return nil
})
a := audit.New(sink, audit.WithQueueSize(2048))
defer a.Stop()
mux.Use(a.HTTPMiddleware(func(r *http.Request) (audit.Resource, string, string) {
    return resUser, r.URL.Query().Get("id"), `{"src":"web"}`
}))
// userID 由 auth 中间件注入:audit.WithUserID(ctx, uid)
```

`Resource`/`Action` 为 int 枚举(业务自定义),便于索引。详见 `examples/audit`。

## 速查:pkg/domain/notification（持久/瞬时二分 + 离线拉取）

与 `pkg/router` 互补:router 投在线者,notification 投离线者(存库 + 上线拉取)。
`persistent` 标志区分二分,seq 游标分页避免重复。参考 Nakama `core_notification.go`。

```go
store := notification.New(func(uid string, n *notification.Notification) bool {
    // 在线投:查 presence,调 router.SendToPresenceIDs;不在线返回 false
    return false
}, notification.WithMaxPerUser(256))
store.Send(ctx, &notification.Notification{
    UserID: "u1", Subject: "friend_request", Persistent: true,
})
list := store.List("u1", afterSeq, 50) // 续传:last.Seq 作 afterSeq
store.Delete("u1", id)                  // 删除即已读,无状态机
```

瞬时通知(`Persistent:false`)仅尝试在线投,不存库。详见 `examples/notification`。

## 速查:pkg/domain/tournament（锦标赛:cron 重置 + 时间窗）

薄层封装 `pkg/leaderboard.RankCache`:每周期用 `expiry`(下一次重置点)作
时间窗 key,天然实现"每周期独立榜单",无需显式清榜。参考 Nakama `core_tournament.go`。

```go
tm, _ := tournament.New("daily", leaderboard.SortDescending, "0 0 * * *",
    tournament.WithDuration(24*3600),
    tournament.WithRankCache(sharedRC), // 多锦标赛可共享一个 RankCache
)
tm.Fill(records, true)
rank := tm.Insert(leaderboard.Record{OwnerID: "dave", Score: 2500}, true)
tm.TopN(10); tm.Around("dave", 2)
tm.NextReset()   // time.Time,下一次重置
tm.CurrentExpiry() // int64,当前周期 key
```

cron 解析复用 `robfig/cron/v3`(5 字段:分 时 日 月 周)。详见 `examples/tournament`。

## 速查:pkg/domain/party（无权威小队）

Leader + Members + JoinRequests + 座位预留,成员变更广播快照。与 `pkg/match`
(权威状态机、固定 tick)互补——party 是用户意愿驱动的临时协作组,无 tick。
参考 Nakama `party_handler.go`。

```go
p := party.New("room1", party.Member{UserID: "alice"}, onChange,
    party.WithOpen(false), party.WithMaxSize(4))
p.RequestJoin(party.Member{UserID: "bob"})   // private:进队列(预留座位)
p.Accept("alice", "bob")                      // Leader 审核
p.Remove("alice", "bob")                      // 踢人(成员可自离)
p.Promote("alice", "bob")                     // 转让队长
p.Snapshot()                                  // 不可变快照
// onChange 回调里调 router.SendToStream 广播给全员
```

队长离开自动转让给最早加入的剩余成员;全员离开则 Stopped。座位预留防止
Accept 时超容量。详见 `examples/party`。

## 速查:pkg/domain/storage（版本化 KV + OCC 乐观锁）

owner + collection + key + value + version,version = value 的 MD5。三种写语义:
IfMatch(版本匹配才写)、IfNotExist(仅当不存在)、LastWriteWins(无条件覆盖)。
批量写按 collection→key→owner 排序防死锁,任一失败回滚。懒淘汰超容量删最旧。
参考 Nakama `core_storage.go`。

```go
s := storage.New(storage.WithMaxEntries(10000))
o, _ := s.Write(storage.WriteOp{
    OwnerID: "u1", Collection: "save", Key: "slot1",
    Value: []byte("hello"), Mode: storage.WriteIfNotExist,
    ReadAccess: 0, WriteAccess: 1,
}, 0)
// OCC 更新:带 version,不匹配则 ErrVersionMismatch
s.Write(storage.WriteOp{..., Mode: storage.WriteIfMatch, Version: o.Version}, 0)
// 批量原子写
s.WriteBatch([]storage.WriteOp{...}, 0)
s.Read("u1", "save", "slot1", callerID) // 按 ReadAccess 校验权限
```

权限:ReadAccess 0=私有/1=自己/2=公开;WriteAccess 0=只读/1=可写。详见 `examples/storage`。

## 速查:pkg/domain/relationship（社交图谱:二部有向图）

边模型 (source, dest, state, position, metadata):state 数值即权限级别(无 RBAC),
position=UnixNano 游标分页。单向 block 与好友共存:block 删己方非 block 边,
好友请求前查 block。参考 Nakama `core_group.go` + `core_friend.go`。

```go
g := relationship.New()
g.AddFriend("a", "b", time.Now().UnixNano())       // 双向 active 边
g.AddEdge(relationship.Edge{Source: "a", Destination: "c", State: relationship.StateActive, Position: pos}) // 单向关注
g.Block("a", "d", pos)                              // 单向拉黑,删 a→d 非 block 边
g.Friends("a")                                      // 双向好友(取交集)
g.Outgoing("a", afterPosition, 50, stateFilter)     // 游标分页(降序,较新在前)
g.IsBlocked("a", "d"); g.Edge("a", "b"); g.Count("a", -1)
```

state 常量:Active/Pending/Admin/Owner/Blocked(业务可自定义扩展)。详见 `examples/relationship`。

## 风格约定

十四个包遵循统一约定,便于混用:

- **纯标准库**——除 `pkg/ws/session` 复用 `pkg/ws`(依赖 `coder/websocket`)、
  `pkg/domain/tournament` 复用 `robfig/cron/v3`(cron 解析)外,其余包零第三方依赖,
  可直接复制到任意 Go 项目。
- **命名空间分层**——`pkg/` 放通用原语(会话/在场/路由/排名/调度/审计),
  `pkg/domain/` 放业务实体(账户/通知/派对/锦标赛/存储/关系)。业务实体带
  具体业务语义,归拢 `domain` 便于识别与隔离。
- **泛型 + 函数式 Option**——`type Option func(*config)`,`config` 不导出,
  默认值在 `New` 内设定。
- **context 驱动生命周期**——`Start(ctx)` / `Stop()` / `Wait()`,遵循 beauty
  的反向优雅关闭惯例。
- **并发安全**——所有导出类型可并发使用;背压一律"满则丢弃/降级"而非阻塞。
- **中文包注释**——首行 `// Package xxx ...`,说明场景与设计来源。

## 与既有包的关系

| 既有包 | 关系 |
|--------|------|
| `pkg/ws` | `session` 的底层;不直接用 `pkg/ws` 的 `Handler` 时仍可单独使用 |
| `pkg/stream` | `Broadcaster` 的扇出语义被 `router` 增强(增加定点/按流/攒批) |
| `pkg/chanx` | 无界 channel,`match`/`scheduler` 内部按需采用有界 channel + 降级 |
| `pkg/service/cron` | 与 `scheduler` 互补:cron 按表达式定时,scheduler 按事件 + 可暂停;
  `tournament` 复用其 `robfig/cron` 解析算重置点 |
| `pkg/xgo.Pool` | `beauty.Go` 全局池;这些包的 goroutine 用 `Start/Stop` 自管生命周期 |

## 参考

- 设计来源:[heroiclabs/nakama](https://github.com/heroiclabs/nakama) 的
  `server/match_handler.go` / `session_ws.go` / `tracker.go` / `message_router.go` /
  `leaderboard_rank_cache.go` / `leaderboard_scheduler.go` / `matchmaker.go` /
  `core_wallet.go` / `console_audit.go` / `core_notification.go` /
  `core_tournament.go` / `party_handler.go` / `core_storage.go` /
  `core_group.go` / `core_friend.go`。
- demo:`examples/{match,session,presence,router,leaderboard,scheduler,matchmaker,
  audit,wallet,notification,tournament,party,storage,relationship}/main.go`。
- 测试:各包 `*_test.go`,均通过 `go test -race -count=3`。
