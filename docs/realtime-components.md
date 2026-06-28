# 实时服务组件库（pkg/ · pkg/domain/）

beauty 在 `pkg/ws`（WebSocket 薄封装）和 `pkg/sse`（SSE 封装）之上,提供了一组
**可独立组合**的实时服务原语,覆盖长连接会话、在线状态、消息路由、匹配组队、
排行榜缓存、任务调度、虚拟账户、操作审计、离线通知、周期榜单、临时小队、
版本化存储、社交图谱、会话令牌、DB 错误翻译、可靠 Webhook、断线重连、状态广播、
频道历史、短期 TTL KV 二十一类典型场景,落地为 beauty 风格(泛型 + 函数式 Option + 中文 doc + 纯标准库)。

包按"通用 vs 业务"分两个命名空间:

- **`pkg/`** —— 通用实时原语(不预设业务语义):会话、在场、路由、匹配、排名、调度、审计、
  令牌、DB 错误翻译、Webhook、断线重连、状态广播、短期 TTL KV。
  这些是"频道/路由/状态机/排名/鉴权/错误归一/重连/缓存"级别的工具,不绑定具体业务实体。
- **`pkg/domain/`** —— 业务实体(预设了业务模型):账户、通知、派对、锦标赛、存储、关系、聊天。
  这些包带"业务实体"语义(货币/通知/小队/赛季榜/存档/社交/频道),归拢到 `domain`
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
| `pkg/token` | dual token(JWT HS256)+ 黑名单注销 | 登录态签发 / 续签 / 踢出 | 8295 |
| `pkg/dberr` | DB 错误码翻译(DB-agnostic → *Status) | 仓储层错误归一为业务码 | 8296 |
| `pkg/webhook` | 事件通知 + 幂等去重 + DLQ | 外部系统回调 / at-least-once | 8297 |
| `pkg/resume` | 断线重连在场还原(token+presence) | 掉线不掉状态 / 自动重连 | 8298 |
| `pkg/presence/status` | 状态变化广播给关注者 | 好友上下线通知 / status event | 8299 |
| `pkg/ephemeral` | 短期 TTL KV(纯内存 + 过期清扫) | 验证码 / 临时数据 / 缓存 | 8302 |
| `pkg/afterwork` | 请求级后台任务延寿(waitUntil 语义) | 响应后发邮件 / 写审计 / 触发 webhook | 8303 |
| `pkg/handler` | 声明式 HTTP handler 包装器(auth+inject+afterwork+错误归一化) | 业务函数只写 (ctx,req)=>(resp,err) | 8303 |
| `pkg/ratelimit` | 按键限流(令牌桶 + 滑动窗口)+ HTTP 中间件 | 防刷屏 / API 限流 / 按用户/IP 隔离 | 8304 |
| `pkg/txn` | 跨域事务协调(两阶段提交 Prepare/Commit/Rollback) | 扣钱包+写存档 原子化 / 任一失败全回滚 | 8305 |
| `pkg/ctxkey` | 类型安全 context key(泛型 Key[T]) | 统一各包 contextKey 定义 / 防 key 冲突 | — |

### 业务实体(pkg/domain/)

| 包 | 一句话 | 典型场景 | demo 端口 |
|----|--------|----------|-----------|
| `pkg/domain/wallet` | 不可变账本 + 余额派生(差值更新) | 虚拟货币 / 积分 / 库存 | 8288 |
| `pkg/domain/notification` | 持久/瞬时二分 + 离线拉取 | 离线消息 / 系统通知 | 8290 |
| `pkg/domain/tournament` | 锦标赛(leaderboard + cron 重置) | 赛季榜 / 每日挑战 | 8291 |
| `pkg/domain/party` | 无权威小队(Leader + 加入审核) | 好友开黑 / 临时小队 | 8292 |
| `pkg/domain/storage` | 版本化 KV + OCC 乐观锁 | 游戏存档 / 用户配置 | 8293 |
| `pkg/domain/relationship` | 社交图谱(二部有向图 + 状态编码) | 好友 / 关注 / 拉黑 / 群组 | 8294 |
| `pkg/domain/chat` | 频道持久消息 + 游标分页 | IM 频道历史 / 翻页 | 8300 |
| `pkg/domain/inbox` | 点对点离线消息收件箱(已读/未读 + ACK) | 离线私聊 / 离线赠礼 / 战绩推送 | 8304 |
| `pkg/domain/group` | 群组实体(角色/邀请审核/公告/banlist) | 公会 / 群聊 / 家族 | 8304 |

> 另有 `examples/clan`(端口 8301)演示用 relationship + tournament + wallet 组合出公会语义,不新增包。

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

**HTTP 支线:声明式 handler + 响应后副作用**——业务函数只写
`(ctx, req) => (resp, error)`,`pkg/handler` 负责认证策略(`WithAuth`)、
依赖注入(`WithInject`)、错误归一化(`errors.WriteHTTP`);响应返回后
`pkg/afterwork` 的 `Wait()` 把 `Defer` 投递的副作用(发邮件 / 写审计 /
触发 `pkg/webhook`)跑完。见 `examples/afterwork`。

## 速查:pkg/match（有状态实时会话）

每个会话由独立 goroutine 驱动,固定帧率 tick,输入/成员/信号经 channel
串行消费,状态封装在 goroutine 内无需锁。

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
关闭握手、写超时保护。

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
双向均 O(1)。附 join/leave 事件总线。

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
黑名单可排除写频繁的榜。

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
与 `pkg/service/cron`(按表达式定时)互补——本包按事件 Submit。

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
(用 Bluge,本包轻量化)。

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
`<0` 即超扣,原子回滚。

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
(失败走 logger),异步落盘不阻塞业务。

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
`persistent` 标志区分二分,seq 游标分页避免重复。

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
时间窗 key,天然实现"每周期独立榜单",无需显式清榜。

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
好友请求前查 block。

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

## 速查:pkg/token（dual token + 黑名单注销）

补齐 `pkg/middleware/auth`(只做验证)缺失的"签发/续签/注销"半边。采用 dual token
模式:短命 session(1h)+ 长命 refresh(7d),**独立密钥**签名,泄露 refresh ≠ 伪造 session。
注销走黑名单:按 `tokenID` 注销单会话,或按 `userID` 全局踢出(此前签发的全部失效)。
JWT 签名复用 `github.com/golang-jwt/jwt/v5`(HS256)。

```go
m := token.New(
    token.WithSessionKey([]byte("32-byte-sess-secret")),
    token.WithRefreshKey([]byte("32-byte-refresh-secret-different")),
    token.WithSessionTTL(time.Hour),
    token.WithRefreshTTL(7*24*time.Hour),
)
defer m.Stop()                          // 停止 gc goroutine(幂等)

// 签发 dual token(session + refresh 复用同 tokenID,便于同步注销)。
sess, refresh, _ := m.Issue("u1", "alice", map[string]string{"role":"admin"}, "")

c, err := m.Verify(sess)               // → *Claims{TokenID,UserID,Username,Vars}
m.Revoke(c.TokenID)                    // 单 token 注销(session+refresh 同失效)
m.RevokeAll("u1")                      // 全局踢出(此前签发的全部 ErrKicked)

newSess, _ := m.Refresh(refresh, nil)  // refresh→新 session(复用 tokenID,不产生新会话)
newSess, _ = m.Refresh(refresh, &map[string]string{"role":"user"}) // 覆盖 vars
```

错误:`ErrInvalidToken` / `ErrExpired` / `ErrRevoked` / `ErrKicked`。
与 `pkg/middleware/auth` 组合即完整登录态。详见 `examples/token`。

## 速查:pkg/dberr（DB 错误码翻译）

把数据库驱动错误翻译为 `pkg/errors` 的 `*Status`,让仓储层只抛原生 driver error,
中间件/网关层统一拿到带业务码的错误。翻译分两步:`Driver.Classify(err) → ErrClass`
(DB 无关枚举),再按表映射到 `Code`。各 driver 适配器各自实现 `Classify`,业务层只认 `ErrClass`。

```go
// 实现 Driver 接口对接具体驱动(pgx/mysql/sqlite...),只暴露 Classify。
type myDriver struct{}
func (myDriver) Classify(err error) dberr.ErrClass {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) {
        switch pgErr.Code {
        case "23505": return dberr.ClassUniqueViolation
        case "23503": return dberr.ClassForeignKeyViolation
        case "23502": return dberr.ClassNotNullViolation
        case "40P01": return dberr.ClassDeadlock
        }
    }
    return dberr.ClassUnknown
}

tr := dberr.New(
    dberr.WithDriver(myDriver{}),
    // 可选:覆盖默认映射(默认:Unique/FK/Deadlock→Conflict,NotFound→404,Timeout→504)
    dberr.WithMapping(dberr.ClassUniqueViolation, errors.CodeInvalidArgument),
)

s := tr.Translate(err)                 // → *errors.Status(带 Code + Cause)
s.Code(); s.Cause()                    // CodeConflict;原 err
tr.Is(err, dberr.ClassDeadlock)        // 条件判断
```

通用适配器:`dberr.ErrorIsDriver`(按 `errors.Is` 哨兵归类,适合 `database/sql` 的
`ErrNoRows`/`ErrConnDone`)、`dberr.NoopDriver`(全归 Unknown)。
默认映射:冲突→409、不存在→404、超时→504、连接→503、未知→500。详见 `examples/dberr`。

## 速查:pkg/webhook（事件通知 + 幂等去重 + DLQ）

事件驱动 Webhook:按事件类型过滤、自定义 header、可选 body 模板、可选 HMAC 签名,
异步触发并带指数退避重试。可靠投递增强(可选):**幂等去重**(`EventID` 非空时同一
endpoint+eventID 只投一次)、**投递状态追踪**(`Store` 记录 delivered/failed)、
**DLQ**(重试耗尽入死信队列,可 `Replay` 重放)。

```go
store := webhook.NewMemStore()         // 内存去重 + 状态记录(多进程用 Redis 实现 Store 接口)
dlq := webhook.NewMemDLQ()             // 内存死信队列
n := webhook.New(
    webhook.WithRetries(3),
    webhook.WithBackoff(200*time.Millisecond),
    webhook.WithStore(store),          // 启用幂等去重 + 状态追踪
    webhook.WithDLQ(dlq),              // 启用死信队列
    webhook.WithErrorHandler(func(ep webhook.Endpoint, ev webhook.Event, err error) {
        log.Printf("webhook failed: %s %v", ep.URL, err)
    }),
)
_ = n.Add(webhook.Endpoint{
    URL: "https://example.com/hooks",
    Events: []string{"order.paid"},   // 事件过滤;空=接收全部
    Secret: "whsec",                  // HMAC-SHA256 签名 → X-Webhook-Signature
    BodyTemplate: `{"type":"{{.Type}}"}`, // 空=JSON(Payload)
})

n.Notify(ctx, webhook.Event{Type:"order.paid", EventID:"evt-1", Payload:order})
// 同 EventID 再投一次 → 去重,只投一次
// 失败重试耗尽 → onError + 入 DLQ + Store 记 failed
ok, err := n.Replay(ctx)              // 从 DLQ 取一条重放(成功则出队,失败则重新入队)
store.Records()                       // 投递状态快照
```

接口:`Store`(MarkDelivered/RecordDelivered/RecordFailed)、`DLQ`(Push/Pop/Len)。
内存实现 `MemStore`/`MemDLQ` 开箱即用。详见 `examples/webhook`。

## 速查:pkg/resume（断线重连在场还原）

补齐登录态的最后一块:把 `pkg/token` 与 `pkg/presence` 织成"掉线不掉状态"。
客户端用 refresh token 重连 → 服务端换出 userID + 查还在哪些流 → 回给客户端自动重连。
约定:业务在 `Issue` 时把 `tokenID` 作为 `presence.Track` 的 sessionID(或建立映射),
本包按此约定查询。

```go
r := resume.New(
    resume.WithTokenManager(tm),   // *token.Manager
    resume.WithTracker(tr),         // *presence.Tracker
)
// 断线重连:refresh token → 还原在场快照。
info, err := r.Resolve(refreshToken)   // → PresenceInfo{UserID,TokenID,Streams}
// 用新 sessionID 把流重新登记(模拟客户端用新连接重连)。
r.MarkOnline("new-"+info.TokenID, info.UserID, "alice", info.Streams, false)
// 或不走 token,直接按 sessionID 查在场(业务自管 sessionID 场景)。
info, _ = r.ResolveBySessionID("sess-99")
```

错误透传:`ErrInvalidToken` / `ErrExpired` / `ErrRevoked` / `ErrKicked`(均为 token 包
同名错误别名)+ `ErrNotConfigured`。详见 `examples/resume`。

## 速查:pkg/presence/status（状态变化广播给关注者）

`pkg/presence.Listener` 只在同流内广播 join/leave;本包订阅 presence 事件,查"谁关注了
状态变化的人"(走 `relationship.Watchers` 反向查询),把 status notification 走 `router`
投递给关注者会话。串起 `relationship + presence + router`。

```go
g := relationship.New()
rtr := router.New(regs, nil)
disp := status.New(
    status.WithWatcherFinder(g),     // 反向查关注者(relationship.Graph 已实现 Watchers)
    status.WithNotifier(func(sids []string, p []byte) int {
        return rtr.SendToSessionIDs(sids, router.Message{Data: p, Reliable: true})
    }),
)
// 用 disp.OnPresence 作 presence.Listener:presence 事件 → status 通知。
tr := presence.New(disp.OnPresence, 256)
tr.Track("s1", stream, presence.Meta{UserID:"alice"})  // alice 上线 → 关注者收 online
tr.Untrack("s1", stream, "alice")                       // alice 全部流离开 → 关注者收 offline
// 手动触发(不经 presence 事件):
disp.Dispatch("alice", status.StateOffline, nil)
```

`relationship.Graph.Watchers(userID, stateFilter)` 反向查"谁把 userID 作为 destination
建了非 block 边"。多图谱可叠加(`WithWatcherFinder` 多次调用,自动去重)。
详见 `examples/status`。

## 速查:pkg/domain/chat（频道持久消息 + 游标分页）

与 `pkg/domain/notification` 互补:notification 按 userID 游标(个人离线信),
chat 按 channelID 游标(频道历史)。IM 频道消息需要持久化 + 历史拉取 + 翻页,
区别于 `pkg/match` 的实时(不持久)与 `pkg/router` 的投递(不存历史)。

```go
s := chat.New(chat.WithMaxPerChannel(500))
m := s.Post("room1", "alice", "hi", time.Now().UnixNano())  // → *Message{ID,MsgID}
s.Post("room1", "bob", "yo", now)

s.Latest("room1", 20)        // 最新 20 条(降序)
s.Before("room1", 8, 20)     // msgID<8 的历史(往前翻,降序)
s.After("room1", 5, 20)      // msgID>5 的新消息(增量拉,升序)
s.LastMsgID("room1")         // 最新 msgID(增量拉取游标基点)
s.Count("room1"); s.Delete("room1", m.ID)
```

`MsgID` 频道内单调,超容量删最旧也不回退(参考 notification 的 seq 设计)。
实时投递与持久化解耦:本包只存历史,实时扇出由 `pkg/router` 负责。详见 `examples/chat`。

## 速查:pkg/ephemeral（短期 TTL KV）

`pkg/domain/storage` 的轻量版:不版本化、不持久,纯内存 + 到点自动过期。
用于验证码 / 匹配房间临时数据 / 短期 token 缓存 / 排行榜快照。

```go
s := ephemeral.New()
defer s.Stop()                          // 停止清扫 goroutine(幂等)
s.Set("code:138xxxx", "123456", 5*time.Minute)
v, ok := s.Get("code:138xxxx")          // → ("123456", true);过期返回 (nil,false) 并惰性删除
s.Delete("code:138xxxx"); s.Len()
// ttl<=0 不存储;overwrite 用更短 TTL 会按新 TTL 过期。
```

底层 `map + 单 goroutine 定时清扫 + Get 惰性删除`(参考 `pkg/token` 的 gc 模式)。
value 类型 `any`(像 sync.Map,一个 Store 存多种类型)。详见 `examples/ephemeral`。

## 速查:pkg/afterwork（请求级后台任务延寿 / waitUntil）

响应可以立即返回,但被 `Defer` 注册的后台任务会继续跑完——运行时不会在响应后立刻杀掉它。
与 `pkg/safe.Go` 的区别:safe.Go 是全局 fire-and-forget,无生命周期绑定;
afterwork 把任务绑定到请求 ctx,响应返回后由框架调用 `Wait()` 等待全部跑完(带上限)。

```go
// 中间件接入:为每个请求建 Registry,handler 返回后 Wait()。
h := afterwork.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    afterwork.Defer(r.Context(), func(ctx context.Context) {
        _ = webhook.Notify(ctx, event) // 响应返回后跑完
    })
    w.Write([]byte("ok"))
}))

// 非 HTTP 场景或测试:独立 Registry。
reg := afterwork.New(afterwork.WithDrainTimeout(10*time.Second))
ctx := afterwork.WithRegistry(context.Background(), reg)
afterwork.Defer(ctx, func(context.Context) { /* ... */ })
reg.Wait() // 阻塞至全部完成或 drain timeout
```

要点:任务 panic 被 `pkg/safe` 恢复(可接 `WithPanicHandler`);任务 ctx 用
`context.WithoutCancel` 派生——请求 ctx 取消时任务**不应**被立即杀死,响应后仍跑完。
`Wait()` 幂等;`Stop()` 是 `Wait()` 的别名。详见 `examples/afterwork`。

## 速查:pkg/handler（声明式 HTTP handler 包装器）

把 auth 策略 + 资源注入 +
错误归一化从业务 handler 上移到包装器,业务函数只写 `(ctx, req) => (resp, error)`。
是 `pkg/middleware/auth` + `pkg/afterwork` + `pkg/errors` + DI 组合成的 ergonomic 装饰器。

```go
type CreateReq struct{ Sku string `json:"sku"` }
type CreateResp struct{ OrderID string `json:"order_id"` }

h := handler.New[CreateReq, CreateResp]("POST",
    func(ctx context.Context, req *CreateReq) (*CreateResp, error) {
        user, _ := auth.GetUserFromContext(ctx)          // WithAuth 注入
        db  := handler.MustGet[*sql.DB](ctx, "db")       // WithInject 注入
        id, err := createOrder(ctx, db, user.ID(), req.Sku)
        if err != nil { return nil, err }
        afterwork.Defer(ctx, func(c context.Context) {   // WithAfterwork 挂载
            _ = webhook.Notify(c, orderEvent{id})
        })
        return &CreateResp{OrderID: id}, nil
    },
    handler.WithAuth(myAuthPolicy),       // 认证+授权,User 注入 ctx
    handler.WithInject("db", orderDB),    // 命名依赖注入
    handler.WithAfterwork(),              // 响应后延寿
)
mux.Handle("/orders", h)                  // h 即 http.Handler
```

要点:返回 `*Status` 原样经 `errors.WriteHTTP` 写出;普通 error 兜底 500。
`Get[T](ctx, name)` 取依赖(类型不符返回 ok=false),`MustGet` 启动期早炸。
`WithMethod` 校验方法;nil 响应返回 204。详见 `examples/afterwork`。

## 速查:pkg/ratelimit（按键限流 + HTTP 中间件）

通用横切限流原语,两种算法:`TokenBucket`(固定速率补令牌,允许突发)与
`SlidingWindow`(滑动窗口精确计数)。按 key 隔离(每用户/IP 独立计数),
超限返回 429 + `Retry-After`。与 `pkg/handler` 声明式组合:`WithRatelimit`。

```go
tb := ratelimit.NewTokenBucket(5, 1)         // 突发5,1/s 补
defer tb.Stop()
tb.Allow("user:alice")                       // (true,0) 突发内放行;超限 (false,retryAfter)

// HTTP 中间件:按客户端 IP 限流。
h := ratelimit.Middleware(tb, ratelimit.ClientIP)(myHandler)

// 声明式接入 pkg/handler:限流在 auth 之前(超限不解析 body)。
handler.New("POST", fn, handler.WithRatelimit(tb, byUserID))

// 滑动窗口:50ms 内最多 3 次。
sw := ratelimit.NewSlidingWindow(3, 50*time.Millisecond)
```

后台 gc 清理长时间无活动的 key(默认 5min idle / 1min 扫一次),避免内存泄漏。
`burst<=0`/`rate<=0`/`limit<=0` 视为不限(Allow 永远 true)。详见 `examples/group`。

## 速查:pkg/domain/inbox（点对点离线消息收件箱）

与 `pkg/domain/notification` 互补:notification 是"系统→用户"单向通知(无已读状态),
inbox 是"用户→用户"点对点离线消息,**带已读/未读 + ACK**(像邮件收件箱)。
离线私聊、离线赠礼、离线战绩推送用本包。

```go
s := inbox.New(liveSink)                     // liveSink 可 nil(纯离线留存)
m := s.Send(ctx, "alice", "bob", "chat", `{"text":"hi"}`)
//  → Message{ID, OwnerID:"alice", FromID:"bob", Seq:1, Read:false}

list := s.List("alice", 0, 10)               // 降序最新 N 条;afterSeq=0 取最新
page2 := s.List("alice", list[len-1].Seq, 10) // 向后翻页

n := s.UnreadCount("alice")                  // 红点数
s.MarkRead("alice", 3)                        // 标记 seq<=3 已读,返回新标记数
s.MarkOneRead("alice", 5)                     // 单条 ACK
s.Delete("alice", 3)                          // 删单条
```

`Seq` 在信箱内单调递增,驱逐后不回退(像 notification seq)。`List` 返回值是拷贝,
外部改不影响内部。`WithMaxPerBox` 默认 500。详见 `examples/group`。

## 速查:pkg/domain/group（群组实体:角色/审核/公告/banlist）

把 `examples/clan` 里现场组合的公会语义沉淀成包。群组是一等实体:owner(唯一)/
admin/member 三级角色,申请-审批工作流,公告,最大人数,封禁名单。内部用
`pkg/domain/relationship.Graph` 存成员边(source=groupID,dest=userID,State=角色),
在其上加业务规则(owner 唯一、admin 不能踢同级、banned 不能加入)。

```go
s := group.New()
s.Create(group.Group{ID:"g1", Name:"公会", OwnerID:"alice", MaxMembers:50})
s.Join("g1", "bob")                           // 直接加入
s.Request("g1", "carol")                      // 申请(待审核)
s.Approve("g1", "alice", "carol")             // owner/admin 审批
s.Promote("g1", "alice", "bob")               // 提为 admin(仅 owner)
s.Kick("g1", "alice", "bob")                  // 踢人(admin 不能踢同级/owner)
s.Ban("g1", "alice", "bob")                   // 封禁(立即移除成员关系)
s.TransferOwner("g1", "alice", "bob")         // 转让群主(旧 owner 降 admin)
s.SetAnnouncement("g1", "alice", "欢迎")       // 公告(仅 owner/admin)
owners, admins, members, _ := s.Members("g1") // 按角色分组
```

角色编码复用 relationship 常量(`RoleOwner`/`RoleAdmin`/`RoleMember`/`RolePending`)。
owner 不能直接退出(须先 `TransferOwner`)。详见 `examples/group`——
该 demo 还组合了 `pkg/domain/inbox`(成员间离线私聊)+ `pkg/ratelimit`(发消息限流)。

## 速查:pkg/txn（跨域事务协调 / 两阶段提交）

让 wallet/storage/notification 等域包在一个逻辑事务边界内原子提交或全部回滚。
各域无需感知 txn——业务层实现 `Participant` 接口(Prepare/Commit/Rollback)
并 `Enlist` 到 `Coordinator`,`Run` 负责两阶段编排:

```go
coord := txn.New()
coord.Enlist("wallet", walletStaging)   // 实现 Participant
coord.Enlist("bag", bagStaging)
err := coord.Run(ctx, func() error {
    // 在 staging 视图上操作(Prepare 已深拷贝),不直接改主库
    if walletStaging.gold < price { return errors.New("没钱") } // 触发 Rollback
    walletStaging.gold -= price
    bagStaging.items[item]++
    return nil
})
// Run 返回 nil → 已 Commit;返回 err → 已 Rollback(主库不变)
```

阶段顺序:Prepare(顺序,任一失败逆序回滚已 Prepare 的)→ body → Commit(顺序,
best-effort:某域 Commit 失败仍继续后续,返回聚合错误供补偿)。Run 串行(一次一事务)。
`ParticipantFunc` 是函数形式(轻量)。`examples/txn` 演示内存快照 staging。

## 速查:pkg/ctxkey（类型安全 context key）

统一各包重复的 `type contextKey struct{}` + `ctx.Value(k).(T)` 模式。泛型
`Key[T]` 编译期约束存取类型,`New[T]()` 每次分配独立标识(同 T 多 Key 不冲突):

```go
var userKey = ctxkey.New[auth.User]()       // 包级 var 调一次
ctx = ctxkey.With(ctx, userKey, user)       // 注入
u, ok := ctxkey.Get(ctx, userKey)           // 取出(类型安全)
u := ctxkey.MustGet(ctx, userKey)           // 不存在返回零值
```

beauty 的 auth/requestid/callbacks/ratelimit/audit/afterwork/metadata/errors
均已改用本包,消除手写类型断言与 key 冲突风险。

## 速查:examples/clan（用现有原语组合出公会,不新增包）

证明 `pkg/` 原语组合已覆盖公会场景,无需新包:
- `relationship` 做成员与角色(leader=StateOwner / member=StateActive);
- `tournament` 做公会战赛季榜(cron 重置 + 排名);
- `wallet` 做公会基金(捐赠/发放);
- `party` 做公会内小队(临时组队)。

路由:`/create` `/join` `/members` `/donate` `/fund` `/score` `/ranking`。详见 `examples/clan`。

## 风格约定

二十一个包遵循统一约定,便于混用:

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

- demo:`examples/{match,session,presence,router,leaderboard,scheduler,matchmaker,
  audit,wallet,notification,tournament,party,storage,relationship,token,dberr,webhook,
  resume,status,chat,ephemeral,clan,afterwork,group,txn}/main.go`。
- 测试:各包 `*_test.go`,均通过 `go test -race -count=3`。
