# Realtime Service Component Library (pkg/ · pkg/domain/)

On top of `pkg/transport/ws` (WebSocket thin wrapper) and `pkg/transport/sse` (SSE wrapper), beauty provides a set of
**independently composable** realtime service primitives covering long-lived connection sessions, online presence,
message routing, matchmaking, leaderboard caching, task scheduling, virtual accounts, operation audit, offline
notifications, periodic leaderboards, temporary squads, versioned storage, social graphs, session tokens,
DB error translation, reliable webhooks, reconnect resume, status broadcast, channel history, short-TTL KV, and more.
It also extends a batch of cross-cutting primitives for **concurrency control / reliability / games & live streaming / spatial geography**
(idempotency, fine-grained locks, backoff, Saga, event bus, delay queue, counter aggregation, Snowflake IDs,
state machines, live PK, combo heat, A* pathfinding, spatial indexing, Geohash), implemented in the beauty style
(generics + functional Options + English docs + stdlib only).

Packages are split into two namespaces by "generic vs domain":

- **`pkg/`** — Generic realtime primitives (no preset business semantics): session, presence, routing, matchmaking,
  ranking, scheduling, audit, tokens, DB error translation, webhooks, reconnect resume, status broadcast, short-TTL KV.
  These are tools at the "channel/routing/state machine/ranking/auth/error normalization/reconnect/cache" level,
  not bound to specific business entities.
- **`pkg/domain/`** — Business entities (preset business models): account, notification, party, tournament, storage,
  relationship, chat. These packages carry "business entity" semantics (currency/notification/squad/seasonal leaderboard/save/social/channel),
  grouped under `domain` for clarity and isolation; import paths are uniformly `pkg/domain/<name>`.

Each package has a single responsibility with no tight coupling: you can use `session` alone for an echo room,
or chain `presence` + `router` + `session` for an IM channel, use `match` + `matchmaker` for an authoritative
match lobby, then add `domain/wallet` + `domain/notification` + `audit` for accounts, notifications, and compliance.
This document gives a quick reference and composition patterns for each package.

## Component Overview

### Generic Primitives (pkg/)

| Package | One-liner | Typical Use | Demo Port |
|----|--------|----------|-----------|
| `pkg/game/match` | Stateful realtime session primitive (actor model) | Game rooms / authoritative battles / collaborative editing | 8181 |
| `pkg/transport/ws/session` | High-level WebSocket stateful session wrapper | Long-connection business / IM 1:1 chat | 8282 |
| `pkg/transport/presence` | Dual-index online presence + event bus | Channel members / online broadcast / candidate pool | 8283 |
| `pkg/transport/router` | Multi-semantic message routing + batching | Broadcast / targeted delivery / batch send | 8284 |
| `pkg/game/leaderboard` | In-memory leaderboard ranking cache (heap sort) | "My rank" / TopN high-frequency reads | 8285 |
| `pkg/orchestration/scheduler` | Worker pool + runtime Pause/Resume | Rewards / batch notifications / expiry cleanup | 8286 |
| `pkg/game/matchmaker` | Attribute-based matchmaking | PVP teams / match lobby | 8287 |
| `pkg/api/audit` | Operation audit (success-only + async persistence) | Compliance / ops audit | 8289 |
| `pkg/api/token` | Dual token (JWT HS256) + blacklist revocation | Login issue / refresh / kick | 8295 |
| `pkg/api/dberr` | DB error translation (DB-agnostic → *Status) | Normalize repo errors to business codes | 8296 |
| `pkg/messaging/webhook` | Event notification + idempotent dedup + DLQ | External callbacks / at-least-once | 8297 |
| `pkg/transport/resume` | Reconnect presence restore (token+presence) | Disconnect without losing state / auto reconnect | 8298 |
| `pkg/transport/presence/status` | Broadcast status changes to watchers | Friend online/offline notifications / status events | 8299 |
| `pkg/store/ephemeral` | Short-TTL KV (in-memory + expiry sweep) | Verification codes / temp data / cache | 8302 |
| `pkg/api/afterwork` | Request-scoped background task extension (waitUntil semantics) | Send email after response / write audit / trigger webhook | 8303 |
| `pkg/api/handler` | Declarative HTTP handler wrapper (auth+inject+afterwork+error normalization) | Business functions only write (ctx,req)=>(resp,err) | 8303 |
| `pkg/resilience/ratelimit` | Key-based rate limiting (token bucket + sliding window) + HTTP middleware | Anti-spam / API rate limit / per-user/IP isolation | 8304 |
| `pkg/orchestration/txn` | Cross-domain transaction coordination (2PC Prepare/Commit/Rollback) | Atomic wallet debit + save write / rollback all on any failure | 8305 |
| `pkg/store/loadbalance` | Load balancing algorithms (consistent hash + smooth weighted round-robin + round-robin) | Session stickiness / stateful sharding / capacity-based dispatch | 8306 |
| `pkg/foundation/ctxkey` | Type-safe context keys (generic Key[T]) | Unify contextKey definitions across packages / prevent key collisions | — |

### Extended Primitives: Concurrency / Reliability / Games & Live Streaming (pkg/)

Beyond the four layers of "connection ↔ session ↔ presence ↔ routing", the following primitives address
**concurrency control, correctness, and game/live-streaming gameplay** as cross-cutting concerns. All are stdlib-only,
generics + functional Options, usable independently or combined with the table above.

| Package | One-liner | Typical Use | Demo |
|----|--------|----------|------|
| **Concurrency & Reliability** | | | |
| `pkg/store/idempotency` | Idempotent execution (dedup + singleflight merge + TTL) | Prevent duplicate charges/rewards · request dedup · cache stampede protection | ✓ |
| `pkg/foundation/keyedmutex` | Fine-grained per-key locks (ref-count auto reclaim) | Serialize same account/room/order · parallel across different entities | ✓ |
| `pkg/resilience/backoff` | Exponential backoff + jitter (Full/Equal/None/proportional) | Retry reliability · spread retry storms | ✓ |
| `pkg/orchestration/saga` | Cross-service Saga orchestration (forward order + reverse compensation + retry) | Gacha/order/redeem cross-service eventual consistency | ✓ |
| `pkg/messaging/eventbus` | Generic in-process event bus (by topic + callbacks) | Decouple modules by events · one event, many subscribers | ✓ |
| **Scheduling & Counting** | | | |
| `pkg/orchestration/delayqueue` | One-shot delayed trigger at a fixed time (min-heap + cancel/reschedule) | Match countdown · buff expiry · order timeout · match fallback | ✓ |
| `pkg/resilience/counter` | Sliding-window count / time-window quota | Daily gacha cap · per-minute danmaku limit · anti-abuse | ✓ |
| `pkg/store/tally` | High-frequency cumulative aggregation + batch flush | Live likes/gifts · analytics counting (reduce write amplification) | ✓ |
| `pkg/idgen` | Distributed unique IDs (Snowflake, 64-bit trend-increasing) | Match IDs · order numbers · message seq · DB primary keys | ✓ |
| **State Machine & Gameplay** | | | |
| `pkg/foundation/fsm` | Generic finite state machine (transition validation + Enter/Leave hooks) | Match/room/order state flow · prevent illegal transitions | ✓ |
| `pkg/game/versus` | Timed multi-party competitive scoring (countdown + winner + event stream) | Live PK · team battles · quiz · vote rally | ✓ |
| `pkg/game/momentum` | Combo + heat time decay (half-life exponential cooldown) | Live combo effects · real-time heat leaderboard | ✓ |
| **Spatial & Geography** | | | |
| `pkg/game/pathfind` | Grid A* pathfinding (obstacles + weights + diagonal) | Tower defense · SLG · click-to-move · monster chase | ✓ |
| `pkg/game/spatial` | Grid spatial index (Nearby / KNN) | Nearby people · MMO AOI · large-map partitioning | ✓ |
| `pkg/game/spatial/aoi` | AOI visibility set diff (enter/leave/stay) | Incremental sync interest management | — |
| `pkg/game/replicate` | DirtySet + Delta projection (baseline/incremental) | State sync egress | — |
| `pkg/game/snapbuf` | Ring snapshot buffer | Lag compensation rewind | — |
| `pkg/game/inputclock` | Client frame mapping + RTT | Lag compensation | — |
| `pkg/game/lagcomp` | Compensated frame query WorldAt | FPS hit detection | — |
| `pkg/game/gameroom` | Dedicated room FSM | Waiting→Running→Draining | — |
| `pkg/game/gameloop` | Fixed-step tick + input fan-out | Lockstep / state sync skeleton | ✓ |
| `pkg/game/geohash` | Lat/lng geocoding (encode/neighbors/cover query/distance) | LBS nearby people/shops (prefix lookup) | ✓ |

> Demos for these primitives are in `examples/<pkg>/main.go`; single-file, directly `go run`.

**Complementary relationships at a glance** (avoid picking the wrong component):

| Need | Use this | Not this | Because |
|------|--------|--------|------|
| "Execute once per key" | `idempotency` | `keyedmutex` | Former reuses first result; latter executes every time, just serially |
| "Serialize every execution per key" | `keyedmutex` | `idempotency` | See above, reversed |
| "Cumulative count within window (quota)" | `counter` | `ratelimit` | ratelimit controls rate (token bucket); counter controls total |
| "Reduce write amplification from high-frequency writes" | `tally` | `wallet` | wallet is per-entry precise ledger; tally aggregates, batches, tolerates tail loss |
| "Real-time heat with decay" | `momentum` | `counter`/`leaderboard` | Latter two don't decay; momentum reflects "how hot right now" |
| "One-shot trigger at fixed time" | `delayqueue` | `scheduler`/`cron` | scheduler is immediate; cron is periodic; delayqueue is one-shot |
| "Atomic across domains in same process" | `txn` | `saga` | txn is in-process 2PC (rollbackable); saga is cross-service compensation |
| "Cross-service eventual consistency" | `saga` | `txn` | See above, reversed |
| "Planar map coordinates" | `spatial` | `geohash` | spatial is planar x/y (game maps); geohash is Earth lat/lng (LBS) |
| "Real lat/lng LBS" | `geohash` | `spatial` | See above, reversed |
| "Single-source fan-out via channel" | `stream` | `eventbus` | stream gives all subscribers the same stream; eventbus is multi-topic callback-style |
| "Multi-topic event decoupling" | `eventbus` | `stream` | See above, reversed |

### Business Entities (pkg/domain/)

| Package | One-liner | Typical Use | Demo Port |
|----|--------|----------|-----------|
| `pkg/domain/wallet` | Immutable ledger + derived balance (delta updates) | Virtual currency / points / inventory | 8288 |
| `pkg/domain/notification` | Persistent/ephemeral split + offline pull | Offline messages / system notifications | 8290 |
| `pkg/domain/tournament` | Tournament (leaderboard + cron reset) | Seasonal leaderboard / daily challenge | 8291 |
| `pkg/domain/party` | Non-authoritative squad (Leader + join approval) | Friend squads / temporary teams | 8292 |
| `pkg/domain/storage` | Versioned KV + OCC optimistic locking | Game saves / user config | 8293 |
| `pkg/domain/relationship` | Social graph (bipartite directed graph + state encoding) | Friends / follows / blocks / groups | 8294 |
| `pkg/domain/chat` | Channel persistent messages + cursor pagination | IM channel history / paging | 8300 |
| `pkg/domain/inbox` | P2P offline message inbox (read/unread + ACK) | Offline DM / offline gifts / match result push | 8304 |
| `pkg/domain/group` | Group entity (roles/invite approval/announcements/banlist) | Guilds / group chat / clans | 8304 |

> Also `examples/clan` (port 8301) demonstrates guild semantics composed from relationship + tournament + wallet, without adding a new package.

> Demo source is in `examples/<pkg>/main.go`, single file, ~50 lines, directly `go run`.

## Composition Patterns

The key to realtime business is the four layers: "connection ↔ session ↔ presence ↔ routing". Each package solves
only one layer; composition works as follows:

```
        ┌─────────────── WebSocket / gRPC long connection ───────────────┐
        │                                                                 │
   pkg/transport/ws/session  ──(Handler.OnOpen/OnMessage/OnClose)──►  Application
        │                                                                 │
        │  Track/Untrack                  Send/QueueDeferred
        ▼                                ▼
   pkg/transport/presence  ◄────── Lookup ──────  pkg/router
   (session↔stream dual index)         (route by presence ID / stream / all)
   
   pkg/game/match          pkg/game/matchmaker         pkg/game/leaderboard     pkg/orchestration/scheduler
   (stateful room)    (team matchmaking)     (rank cache)        (background worker pool)
        │                   │                      │                   │
        └─── Subscribe ───► Application ◄── Match callback ─┘ ── Insert ───────┘

   ┌─── Cross-cutting branch (pkg/domain/ business entities)──────────────────┐
   │  domain/wallet      domain/notification      audit                        │
   │  (delta ledger)       (persistent/ephemeral + offline)  (success-only)    │
   │      ▲                    ▲ liveSink              ▲ wrap(HTTP)            │
   │      │                    │                        │                      │
   │      └── debit/reward ◄── Application ──► offline retention ──► persist success │
   │                                                                            │
   │  domain/tournament (leaderboard+cron)  domain/party (non-authoritative squad) │
   │  domain/storage (versioned KV+OCC)  domain/relationship (social graph)      │
   └────────────────────────────────────────────────────────────────────────────┘
```

**Minimal composition: stateful room** — `session` connects to `match`; `match.Tick` output is broadcast via
`session.Send` (see `examples/match`).

**Typical composition: IM channel** — `session` maintains connections, `presence` registers presence, `router`
broadcasts by stream. On client `/join`: `presence.Track` + register `router.Sink`; on `/say`: `router.SendToStream`
(see `examples/router` + `examples/presence`).

**Account branch: rewards + notifications + audit** — On match end, `wallet.Apply` pays team members
(atomic overdraft protection), `notification.Send` pushes "reward credited" (persisted if offline), `audit`
middleware records "admin grant reward" throughout (see `examples/wallet` + `notification` + `audit`).

**Periodic leaderboard: tournament** — `tournament.New("daily", desc, "0 0 * * *")` rolls a new period daily at midnight;
`Insert/TopN` automatically land on the current period's `leaderboard.RankCache`
(see `examples/tournament`).

**Full composition: match lobby** — `matchmaker.Add` enqueues; in match callback, `presence.Track` adds members to
the same stream and `match.Start` opens a room; room output is delivered via `router.SendToStream`.

**HTTP branch: declarative handler + post-response side effects** — Business functions only write
`(ctx, req) => (resp, error)`; `pkg/api/handler` handles auth policy (`WithAuth`), dependency injection (`WithInject`),
error normalization (`errors.WriteHTTP`); after response returns, `pkg/api/afterwork`'s `Wait()` runs all `Defer`-queued
side effects (send email / write audit / trigger `pkg/messaging/webhook`). See `examples/afterwork`.

**Live streaming composition: multi-room PK** — Build a multi-room live PK backend with extended primitives: each match
uses `versus` for timed competitive scoring and winner determination; multiple matches in parallel via roomID→Match map;
`keyedmutex` serializes structural ops like start/settle per room while rooms don't block each other; `idgen`
generates room IDs. High-frequency gift requests go through `counter` (per-user per-minute gift quota, anti-abuse)
+ `idempotency` (idempotency key dedup, prevent duplicate gift charges); converted scores go to `versus.Add`,
while `tally` aggregates high-frequency "likes/popularity" counts for batch persistence; per-room score changes are
bridged via `versus`'s event stream (internally reuses `stream`) to SSE clients; global PK lifecycle (start/end) is
broadcast via `eventbus` to decouple notification/leaderboard downstream modules. See `examples/live-pk` (composition demo).

## Quick Reference: pkg/game/match (Stateful Realtime Session)

Each session is driven by a dedicated goroutine with fixed tick rate; input/members/signals are consumed serially
via channels; state is encapsulated inside the goroutine without locks.

```go
m := match.New[GameState, Input, Msg](myHandler, nil,
    match.WithTickRate(20),          // Hz
    match.WithInputQueue(256),
    match.WithMaxIdleSec(60),
)
m.Start(ctx)
m.QueueInput(in)                     // non-blocking; drop when queue full
out, cancel := m.Subscribe(ctx)      // subscribe to Tick output
m.Stop(); m.Wait()                   // graceful stop
```

Business implements `Handler.Init/Tick`. Backpressure: `QueueInput` full → drop + warn; call queue full →
treated as overload stop. See `examples/match`.

## Quick Reference: pkg/transport/ws/session (WebSocket Session Wrapper)

Production-grade capabilities on top of the `pkg/transport/ws` thin wrapper: dual goroutine read/write separation,
periodic Ping heartbeat, close handshake, write timeout protection.

```go
mux.Handle("/ws", ws.Handler(session.Accept(&myHandler{},
    session.WithPingPeriod(30*time.Second),
    session.WithPingTimeout(5*time.Second),
), ws.WithInsecureSkipVerify()))
```

Business implements `Handler.OnOpen/OnMessage/OnClose`; use `s.Send/SendText/SendJSON` for writes.
Queue full auto-closes slow clients. See `examples/session`.

## Quick Reference: pkg/transport/presence (Dual-Index Online Presence)

Maintains bidirectional index of "who is in which stream": lookup members by stream (for broadcast), lookup stream
by session (for offline cleanup); both directions O(1). Includes join/leave event bus.

```go
tr := presence.New(func(stream presence.Stream, joins, leaves []*presence.Presence) {
    // event callback
}, 256)
tr.Track(sid, stream, presence.Meta{UserID: uid})
members := tr.ListByStream(stream, false)
tr.UntrackAll(sid)                    // one-click cleanup on session offline
```

Concurrency-safe. Event queue full → drop (non-blocking). See `examples/presence`.

## Quick Reference: pkg/transport/router (Multi-Semantic Message Routing)

Enhanced `Broadcaster`: targeted delivery by presence ID, broadcast by stream, batch send.

```go
rtr := router.New(registry, tr)       // registry: sessionID→Sink, tr: presence.Tracker
rtr.SendToPresenceIDs(ids, msg, true)
n := rtr.SendToStream(stream, msg, false)  // lookup members via presence
rtr.QueueDeferred(sids, msg); rtr.FlushDeferred()  // batch
rtr.SendToAll(msg)
```

`FlushDeferred` sends in batch per session, reducing Lookups. See `examples/router`.

## Quick Reference: pkg/game/leaderboard (Leaderboard Ranking Cache)

Heap sort maintains ordered structure per board; O(log N) for "my rank", TopN, fetch by rank;
blacklist can exclude write-heavy boards.

```go
rc := leaderboard.New()
rc.Fill("score", 0, leaderboard.SortDescending, records, true)
rank := rc.Get("score", 0, userID)               // lookup rank
top := rc.TopN("score", 0, 10)
newRank := rc.Insert("score", 0, leaderboard.SortDescending, rec, true)
rc.Delete("score", 0, userID)
```

Concurrency-safe. `Fill` is idempotent (safe to reload). See `examples/leaderboard`.

## Quick Reference: pkg/orchestration/scheduler (Worker Pool + Pause/Resume)

N workers consume queue concurrently; supports runtime Pause/Resume and graceful stop; worker panic auto-recovers.
Complements `pkg/service/cron` (expression-based scheduling) — this package Submit on events.

```go
s := scheduler.New(
    scheduler.WithWorkers(3),
    scheduler.WithQueueSize(100),
    scheduler.WithErrorHandler(handler),
)
s.Start(ctx)
s.Submit(&scheduler.Task{Name: "work", Fn: fn})
s.Pause(); s.Resume()                // runtime control
s.Stop(); s.Wait()                   // graceful stop
```

`WithWorkers(0)` allows pure queue mode (enqueue only, no consumption). See `examples/scheduler`.

## Quick Reference: pkg/game/matchmaker (Attribute-Based Team Matchmaking)

Players register tickets with string+numeric attributes; matcher uses "bucket (region+mode) + skill-sorted greedy"
to form teams; callback when complete. Stdlib-only; suitable for single-node tens of thousands of tickets.
(Uses Bluge; this package is lightweight).

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

Timeout relaxes bucket constraints (`maxWaitSec`) to avoid long waits. See `examples/matchmaker`.

## Quick Reference: pkg/domain/wallet (Immutable Ledger + Derived Balance)

Dual model: current balance (fast read) + append-only ledger (auditable/replayable). Changeset delta updates;
`<0` means overdraft, atomic rollback.

```go
w := wallet.New()
bal, l, err := w.Apply("u1", wallet.WalletMap{"gold": 100}, "init", now)
// Debit: negative delta; insufficient balance → ErrInsufficientBalance (rollback, no ledger append)
w.Apply("u1", wallet.WalletMap{"gold": -50}, "spend", now)
w.Balance("u1")      // {gold:50}
w.Ledgers("u1")      // full ledger
w.LedgerByID("u1", l.ID)
w.SetBalance("u1", WalletMap{"gold": 999}) // restore from DB on startup, no ledger entry
```

Concurrency-safe. See `examples/wallet`.

## Quick Reference: pkg/api/audit (Operation Audit, Success-Only)

Structured record of "who did what to which resource"; only records successful operations where `err==nil` and status < 500
(failures go to logger); async persistence does not block business.

```go
sink := audit.SinkFunc(func(ctx context.Context, e audit.Entry) error {
    // write to DB / file
    return nil
})
a := audit.New(sink, audit.WithQueueSize(2048))
defer a.Stop()
mux.Use(a.HTTPMiddleware(func(r *http.Request) (audit.Resource, string, string) {
    return resUser, r.URL.Query().Get("id"), `{"src":"web"}`
}))
// userID injected by auth middleware: audit.WithUserID(ctx, uid)
```

`Resource`/`Action` are int enums (business-defined) for indexing. See `examples/audit`.

## Quick Reference: pkg/domain/notification (Persistent/Ephemeral Split + Offline Pull)

Complements `pkg/transport/router`: router delivers to online users; notification delivers to offline users (store + pull on reconnect).
`persistent` flag splits the two; seq cursor pagination avoids duplicates.

```go
store := notification.New(func(uid string, n *notification.Notification) bool {
    // live delivery: check presence, call router.SendToPresenceIDs; return false if offline
    return false
}, notification.WithMaxPerUser(256))
store.Send(ctx, &notification.Notification{
    UserID: "u1", Subject: "friend_request", Persistent: true,
})
list := store.List("u1", afterSeq, 50) // resume: use last.Seq as afterSeq
store.Delete("u1", id)                  // delete = read, no state machine
```

Ephemeral notifications (`Persistent:false`) only attempt live delivery, not stored. See `examples/notification`.

## Quick Reference: pkg/domain/tournament (Tournament: Cron Reset + Time Window)

Thin wrapper over `pkg/game/leaderboard.RankCache`: each period uses `expiry` (next reset point) as time-window key,
naturally implementing "independent leaderboard per period" without explicit clear.

```go
tm, _ := tournament.New("daily", leaderboard.SortDescending, "0 0 * * *",
    tournament.WithDuration(24*3600),
    tournament.WithRankCache(sharedRC), // multiple tournaments can share one RankCache
)
tm.Fill(records, true)
rank := tm.Insert(leaderboard.Record{OwnerID: "dave", Score: 2500}, true)
tm.TopN(10); tm.Around("dave", 2)
tm.NextReset()   // time.Time, next reset
tm.CurrentExpiry() // int64, current period key
```

Cron parsing reuses `robfig/cron/v3` (5 fields: min hour day month weekday). See `examples/tournament`.

## Quick Reference: pkg/domain/party (Non-Authoritative Squad)

Leader + Members + JoinRequests + seat reservation; member changes broadcast snapshot. Complements `pkg/game/match`
(authoritative state machine, fixed tick) — party is user-intent-driven temporary collaboration, no tick.

```go
p := party.New("room1", party.Member{UserID: "alice"}, onChange,
    party.WithOpen(false), party.WithMaxSize(4))
p.RequestJoin(party.Member{UserID: "bob"})   // private: enters queue (seat reserved)
p.Accept("alice", "bob")                      // Leader approval
p.Remove("alice", "bob")                      // kick (members can leave themselves)
p.Promote("alice", "bob")                     // transfer leader
p.Snapshot()                                  // immutable snapshot
// in onChange callback, call router.SendToStream to broadcast to all
```

Leader leaving auto-transfers to earliest remaining member; all leave → Stopped. Seat reservation prevents
over-capacity on Accept. See `examples/party`.

## Quick Reference: pkg/domain/storage (Versioned KV + OCC Optimistic Locking)

owner + collection + key + value + version; version = MD5 of value. Three write semantics:
IfMatch (write only if version matches), IfNotExist (only when absent), LastWriteWins (unconditional overwrite).
Batch writes sorted by collection→key→owner to prevent deadlock; any failure rolls back. Lazy eviction deletes oldest when over capacity.

```go
s := storage.New(storage.WithMaxEntries(10000))
o, _ := s.Write(storage.WriteOp{
    OwnerID: "u1", Collection: "save", Key: "slot1",
    Value: []byte("hello"), Mode: storage.WriteIfNotExist,
    ReadAccess: 0, WriteAccess: 1,
}, 0)
// OCC update: include version; mismatch → ErrVersionMismatch
s.Write(storage.WriteOp{..., Mode: storage.WriteIfMatch, Version: o.Version}, 0)
// atomic batch write
s.WriteBatch([]storage.WriteOp{...}, 0)
s.Read("u1", "save", "slot1", callerID) // ReadAccess permission check
```

Permissions: ReadAccess 0=private/1=self/2=public; WriteAccess 0=read-only/1=writable. See `examples/storage`.

## Quick Reference: pkg/domain/relationship (Social Graph: Bipartite Directed Graph)

Edge model (source, dest, state, position, metadata): state value is permission level (no RBAC);
position=UnixNano for cursor pagination. Unidirectional block coexists with friends: block removes non-block edges
from self; check block before friend request.

```go
g := relationship.New()
g.AddFriend("a", "b", time.Now().UnixNano())       // bidirectional active edges
g.AddEdge(relationship.Edge{Source: "a", Destination: "c", State: relationship.StateActive, Position: pos}) // unidirectional follow
g.Block("a", "d", pos)                              // unidirectional block, removes a→d non-block edges
g.Friends("a")                                      // bidirectional friends (intersection)
g.Outgoing("a", afterPosition, 50, stateFilter)     // cursor pagination (desc, newer first)
g.IsBlocked("a", "d"); g.Edge("a", "b"); g.Count("a", -1)
```

State constants: Active/Pending/Admin/Owner/Blocked (business can extend). See `examples/relationship`.

## Quick Reference: pkg/api/token (Dual Token + Blacklist Revocation)

Completes the missing "issue/refresh/revoke" half of `pkg/middleware/auth` (validation only). Dual token mode:
short-lived session (1h) + long-lived refresh (7d), **independent keys** for signing; refresh leak ≠ session forgery.
Revocation via blacklist: revoke single session by `tokenID`, or global kick by `userID` (all previously issued invalid).
JWT signing reuses `github.com/golang-jwt/jwt/v5` (HS256).

```go
m := token.New(
    token.WithSessionKey([]byte("32-byte-sess-secret")),
    token.WithRefreshKey([]byte("32-byte-refresh-secret-different")),
    token.WithSessionTTL(time.Hour),
    token.WithRefreshTTL(7*24*time.Hour),
)
defer m.Stop()                          // stop gc goroutine (idempotent)

// Issue dual token (session + refresh share same tokenID for synchronized revocation).
sess, refresh, _ := m.Issue("u1", "alice", map[string]string{"role":"admin"}, "")

c, err := m.Verify(sess)               // → *Claims{TokenID,UserID,Username,Vars}
m.Revoke(c.TokenID)                    // single token revoke (session+refresh both invalid)
m.RevokeAll("u1")                      // global kick (all previously issued → ErrKicked)

newSess, _ := m.Refresh(refresh, nil)  // refresh→new session (reuse tokenID, no new session)
newSess, _ = m.Refresh(refresh, &map[string]string{"role":"user"}) // override vars
```

Errors: `ErrInvalidToken` / `ErrExpired` / `ErrRevoked` / `ErrKicked`.
Combined with `pkg/middleware/auth` for complete login state. See `examples/token`.

## Quick Reference: pkg/api/dberr (DB Error Translation)

Translates database driver errors to `pkg/api/errors` `*Status`, so repo layer only throws native driver errors
and middleware/gateway gets business-coded errors uniformly. Two steps: `Driver.Classify(err) → ErrClass`
(DB-agnostic enum), then map to `Code` per table. Each driver adapter implements `Classify`; business layer only knows `ErrClass`.

```go
// Implement Driver interface for specific driver (pgx/mysql/sqlite...), only expose Classify.
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
    // optional: override default mapping (default: Unique/FK/Deadlock→Conflict, NotFound→404, Timeout→504)
    dberr.WithMapping(dberr.ClassUniqueViolation, errors.CodeInvalidArgument),
)

s := tr.Translate(err)                 // → *errors.Status (with Code + Cause)
s.Code(); s.Cause()                    // CodeConflict; original err
tr.Is(err, dberr.ClassDeadlock)        // conditional check
```

Generic adapters: `dberr.ErrorIsDriver` (classify by `errors.Is` sentinels, suitable for `database/sql`'s
`ErrNoRows`/`ErrConnDone`), `dberr.NoopDriver` (all Unknown).
Default mapping: conflict→409, not found→404, timeout→504, connection→503, unknown→500. See `examples/dberr`.

## Quick Reference: pkg/messaging/webhook (Event Notification + Idempotent Dedup + DLQ)

Event-driven webhooks: filter by event type, custom headers, optional body template, optional HMAC signature,
async trigger with exponential backoff retry. Reliable delivery enhancements (optional): **idempotent dedup**
(when `EventID` non-empty, same endpoint+eventID delivered once), **delivery status tracking**
(`Store` records delivered/failed), **DLQ** (exhausted retries go to dead letter queue, `Replay` available).

```go
store := webhook.NewMemStore()         // in-memory dedup + status (multi-process: implement Store with Redis)
dlq := webhook.NewMemDLQ()             // in-memory dead letter queue
n := webhook.New(
    webhook.WithRetries(3),
    webhook.WithBackoff(200*time.Millisecond),
    webhook.WithStore(store),          // enable idempotent dedup + status tracking
    webhook.WithDLQ(dlq),              // enable dead letter queue
    webhook.WithErrorHandler(func(ep webhook.Endpoint, ev webhook.Event, err error) {
        log.Printf("webhook failed: %s %v", ep.URL, err)
    }),
)
_ = n.Add(webhook.Endpoint{
    URL: "https://example.com/hooks",
    Events: []string{"order.paid"},   // event filter; empty = all
    Secret: "whsec",                  // HMAC-SHA256 signature → X-Webhook-Signature
    BodyTemplate: `{"type":"{{.Type}}"}`, // empty = JSON(Payload)
})

n.Notify(ctx, webhook.Event{Type:"order.paid", EventID:"evt-1", Payload:order})
// same EventID again → deduped, delivered once
// retry exhausted → onError + DLQ + Store records failed
ok, err := n.Replay(ctx)              // replay one from DLQ (success dequeues, failure re-enqueues)
store.Records()                       // delivery status snapshot
```

Interfaces: `Store` (MarkDelivered/RecordDelivered/RecordFailed), `DLQ` (Push/Pop/Len).
In-memory `MemStore`/`MemDLQ` work out of the box. See `examples/webhook`.

## Quick Reference: pkg/transport/resume (Reconnect Presence Restore)

Completes the last piece of login state: weaves `pkg/api/token` and `pkg/transport/presence` into "disconnect without losing state".
Client reconnects with refresh token → server resolves userID + which streams still active → returns to client for auto reconnect.
Convention: business passes `tokenID` as `presence.Track` sessionID (or maintains mapping) at `Issue`; this package queries by that convention.

```go
r := resume.New(
    resume.WithTokenManager(tm),   // *token.Manager
    resume.WithTracker(tr),         // *presence.Tracker
)
// Reconnect: refresh token → restore presence snapshot.
info, err := r.Resolve(refreshToken)   // → PresenceInfo{UserID,TokenID,Streams}
// Re-register streams with new sessionID (simulate client reconnect with new connection).
r.MarkOnline("new-"+info.TokenID, info.UserID, "alice", info.Streams, false)
// Or skip token, lookup presence by sessionID directly (business-managed sessionID).
info, _ = r.ResolveBySessionID("sess-99")
```

Errors pass through: `ErrInvalidToken` / `ErrExpired` / `ErrRevoked` / `ErrKicked` (aliases of token package
same-name errors) + `ErrNotConfigured`. See `examples/resume`.

## Quick Reference: pkg/transport/presence/status (Broadcast Status Changes to Watchers)

`pkg/transport/presence.Listener` only broadcasts join/leave within the same stream; this package subscribes to presence events,
looks up "who watches the person whose status changed" (via `relationship.Watchers` reverse lookup), and delivers
status notifications via `router` to watcher sessions. Chains `relationship + presence + router`.

```go
g := relationship.New()
rtr := router.New(regs, nil)
disp := status.New(
    status.WithWatcherFinder(g),     // reverse lookup watchers (relationship.Graph implements Watchers)
    status.WithNotifier(func(sids []string, p []byte) int {
        return rtr.SendToSessionIDs(sids, router.Message{Data: p, Reliable: true})
    }),
)
// Use disp.OnPresence as presence.Listener: presence events → status notifications.
tr := presence.New(disp.OnPresence, 256)
tr.Track("s1", stream, presence.Meta{UserID:"alice"})  // alice online → watchers receive online
tr.Untrack("s1", stream, "alice")                       // alice leaves all streams → watchers receive offline
// Manual trigger (without presence event):
disp.Dispatch("alice", status.StateOffline, nil)
```

`relationship.Graph.Watchers(userID, stateFilter)` reverse-looks up "who has userID as destination with non-block edge".
Multiple graphs can be stacked (`WithWatcherFinder` called multiple times, auto dedup).
See `examples/status`.

## Quick Reference: pkg/domain/chat (Channel Persistent Messages + Cursor Pagination)

Complements `pkg/domain/notification`: notification cursors by userID (personal offline inbox);
chat cursors by channelID (channel history). IM channel messages need persistence + history pull + paging,
distinct from `pkg/game/match` realtime (not persistent) and `pkg/transport/router` delivery (no history storage).

```go
s := chat.New(chat.WithMaxPerChannel(500))
m := s.Post("room1", "alice", "hi", time.Now().UnixNano())  // → *Message{ID,MsgID}
s.Post("room1", "bob", "yo", now)

s.Latest("room1", 20)        // latest 20 (desc)
s.Before("room1", 8, 20)     // history with msgID<8 (page back, desc)
s.After("room1", 5, 20)      // new messages with msgID>5 (incremental pull, asc)
s.LastMsgID("room1")         // latest msgID (incremental pull cursor base)
s.Count("room1"); s.Delete("room1", m.ID)
```

`MsgID` monotonic within channel; evicting oldest does not roll back (like notification seq design).
Real-time delivery and persistence decoupled: this package only stores history; real-time fan-out by `pkg/transport/router`. See `examples/chat`.

## Quick Reference: pkg/store/ephemeral (Short-TTL KV)

Lightweight version of `pkg/domain/storage`: no versioning, no persistence, in-memory + auto expiry.
For verification codes / match room temp data / short token cache / leaderboard snapshots.

```go
s := ephemeral.New()
defer s.Stop()                          // stop sweep goroutine (idempotent)
s.Set("code:138xxxx", "123456", 5*time.Minute)
v, ok := s.Get("code:138xxxx")          // → ("123456", true); expired returns (nil,false) and lazy delete
s.Delete("code:138xxxx"); s.Len()
// ttl<=0 not stored; overwrite with shorter TTL expires by new TTL.
```

Underlying `map + single goroutine periodic sweep + Get lazy delete` (like `pkg/api/token` gc pattern).
Value type `any` (like sync.Map, one Store holds multiple types). See `examples/ephemeral`.

## Quick Reference: pkg/api/afterwork (Request-Scoped Background Task Extension / waitUntil)

Response can return immediately, but background tasks registered via `Defer` continue running — runtime won't kill them right after response.
Unlike `pkg/foundation/safe.Go`: safe.Go is global fire-and-forget, no lifecycle binding;
afterwork binds tasks to request ctx; framework calls `Wait()` after response to wait for all (with limit).

```go
// Middleware: create Registry per request; Wait() after handler returns.
h := afterwork.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    afterwork.Defer(r.Context(), func(ctx context.Context) {
        _ = webhook.Notify(ctx, event) // runs after response returns
    })
    w.Write([]byte("ok"))
}))

// Non-HTTP or tests: standalone Registry.
reg := afterwork.New(afterwork.WithDrainTimeout(10*time.Second))
ctx := afterwork.WithRegistry(context.Background(), reg)
afterwork.Defer(ctx, func(context.Context) { /* ... */ })
reg.Wait() // block until all complete or drain timeout
```

Notes: task panic recovered by `pkg/foundation/safe` (can hook `WithPanicHandler`); task ctx derived from
`context.WithoutCancel` — request ctx cancel should **not** kill tasks immediately; they finish after response.
`Wait()` idempotent; `Stop()` is alias of `Wait()`. See `examples/afterwork`.

## Quick Reference: pkg/api/handler (Declarative HTTP Handler Wrapper)

Moves auth policy + resource injection + error normalization off business handlers;
business functions only write `(ctx, req) => (resp, error)`.
Ergonomic decorator combining `pkg/middleware/auth` + `pkg/api/afterwork` + `pkg/api/errors` + DI.

```go
type CreateReq struct{ Sku string `json:"sku"` }
type CreateResp struct{ OrderID string `json:"order_id"` }

h := handler.New[CreateReq, CreateResp]("POST",
    func(ctx context.Context, req *CreateReq) (*CreateResp, error) {
        user, _ := auth.GetUserFromContext(ctx)          // WithAuth injects
        db  := handler.MustGet[*sql.DB](ctx, "db")       // WithInject injects
        id, err := createOrder(ctx, db, user.ID(), req.Sku)
        if err != nil { return nil, err }
        afterwork.Defer(ctx, func(c context.Context) {   // WithAfterwork mounts
            _ = webhook.Notify(c, orderEvent{id})
        })
        return &CreateResp{OrderID: id}, nil
    },
    handler.WithAuth(myAuthPolicy),       // auth+authorize, User injected into ctx
    handler.WithInject("db", orderDB),    // named dependency injection
    handler.WithAfterwork(),              // post-response extension
)
mux.Handle("/orders", h)                  // h is http.Handler
```

Notes: returning `*Status` written as-is via `errors.WriteHTTP`; plain error defaults to 500.
`Get[T](ctx, name)` gets dependency (type mismatch returns ok=false); `MustGet` fails fast at startup.
`WithMethod` validates method; nil response returns 204. See `examples/afterwork`.

## Quick Reference: pkg/resilience/ratelimit (Key-Based Rate Limiting + HTTP Middleware)

Generic cross-cutting rate limit primitive; two algorithms: `TokenBucket` (fixed rate refill, allows burst) and
`SlidingWindow` (sliding window precise count). Isolated per key (each user/IP independent count);
over limit returns 429 + `Retry-After`. Declarative combo with `pkg/api/handler`: `WithRatelimit`.

```go
tb := ratelimit.NewTokenBucket(5, 1)         // burst 5, refill 1/s
defer tb.Stop()
tb.Allow("user:alice")                       // (true,0) within burst; over limit (false,retryAfter)

// HTTP middleware: rate limit by client IP.
h := ratelimit.Middleware(tb, ratelimit.ClientIP)(myHandler)

// Declarative with pkg/api/handler: rate limit before auth (over limit skips body parse).
handler.New("POST", fn, handler.WithRatelimit(tb, byUserID))

// Sliding window: max 3 in 50ms.
sw := ratelimit.NewSlidingWindow(3, 50*time.Millisecond)
```

Background gc cleans long-idle keys (default 5min idle / 1min sweep) to avoid memory leak.
`burst<=0`/`rate<=0`/`limit<=0` treated as unlimited (Allow always true). See `examples/group`.

## Quick Reference: pkg/domain/inbox (P2P Offline Message Inbox)

Complements `pkg/domain/notification`: notification is "system→user" one-way (no read state);
inbox is "user→user" P2P offline messages, **with read/unread + ACK** (like email inbox).
For offline DM, offline gifts, offline match result push.

```go
s := inbox.New(liveSink)                     // liveSink can be nil (offline-only)
m := s.Send(ctx, "alice", "bob", "chat", `{"text":"hi"}`)
//  → Message{ID, OwnerID:"alice", FromID:"bob", Seq:1, Read:false}

list := s.List("alice", 0, 10)               // latest N desc; afterSeq=0 gets latest
page2 := s.List("alice", list[len-1].Seq, 10) // page forward

n := s.UnreadCount("alice")                  // badge count
s.MarkRead("alice", 3)                        // mark seq<=3 read, returns newly marked count
s.MarkOneRead("alice", 5)                     // single ACK
s.Delete("alice", 3)                          // delete one
```

`Seq` monotonic within mailbox; does not roll back after eviction (like notification seq). `List` returns copies,
external changes don't affect internal. `WithMaxPerBox` default 500. See `examples/group`.

## Quick Reference: pkg/domain/group (Group Entity: Roles/Approval/Announcements/Banlist)

Crystallizes guild semantics from `examples/clan` into a package. Group is a first-class entity: owner (unique)/
admin/member three-tier roles, apply-approve workflow, announcements, max members, ban list. Internally uses
`pkg/domain/relationship.Graph` for member edges (source=groupID, dest=userID, State=role),
with business rules on top (unique owner, admin can't kick peer, banned can't join).

```go
s := group.New()
s.Create(group.Group{ID:"g1", Name:"Guild", OwnerID:"alice", MaxMembers:50})
s.Join("g1", "bob")                           // direct join
s.Request("g1", "carol")                      // apply (pending approval)
s.Approve("g1", "alice", "carol")             // owner/admin approval
s.Promote("g1", "alice", "bob")               // promote to admin (owner only)
s.Kick("g1", "alice", "bob")                  // kick (admin can't kick peer/owner)
s.Ban("g1", "alice", "bob")                   // ban (immediately removes membership)
s.TransferOwner("g1", "alice", "bob")         // transfer owner (old owner demoted to admin)
s.SetAnnouncement("g1", "alice", "Welcome")   // announcement (owner/admin only)
owners, admins, members, _ := s.Members("g1") // grouped by role
```

Role encoding reuses relationship constants (`RoleOwner`/`RoleAdmin`/`RoleMember`/`RolePending`).
Owner can't leave directly (must `TransferOwner` first). See `examples/group` —
that demo also combines `pkg/domain/inbox` (offline DM between members) + `pkg/resilience/ratelimit` (message rate limit).

## Quick Reference: pkg/orchestration/txn (Cross-Domain Transaction Coordination / Two-Phase Commit)

Atomically commit or rollback wallet/storage/notification and other domain packages within one logical transaction boundary.
Domains don't need to know about txn — business implements `Participant` interface (Prepare/Commit/Rollback)
and `Enlist`s to `Coordinator`; `Run` orchestrates two-phase:

```go
coord := txn.New()
coord.Enlist("wallet", walletStaging)   // implements Participant
coord.Enlist("bag", bagStaging)
err := coord.Run(ctx, func() error {
    // operate on staging view (Prepare already deep-copied), don't touch main store directly
    if walletStaging.gold < price { return errors.New("insufficient funds") } // triggers Rollback
    walletStaging.gold -= price
    bagStaging.items[item]++
    return nil
})
// Run returns nil → Committed; returns err → Rolled back (main store unchanged)
```

Phase order: Prepare (sequential, any failure rolls back already-Prepared in reverse) → body → Commit (sequential,
best-effort: Commit failure on one domain continues others, returns aggregated error for compensation). Run is serial (one transaction at a time).
`ParticipantFunc` is function form (lightweight). `examples/txn` demonstrates in-memory snapshot staging.

## Quick Reference: pkg/foundation/ctxkey (Type-Safe Context Keys)

Unifies the repeated `type contextKey struct{}` + `ctx.Value(k).(T)` pattern across packages. Generic
`Key[T]` constrains get/set types at compile time; `New[T]()` allocates independent identity each call (same T, multiple Keys don't collide):

```go
var userKey = ctxkey.New[auth.User]()       // package-level var, call once
ctx = ctxkey.With(ctx, userKey, user)       // inject
u, ok := ctxkey.Get(ctx, userKey)           // retrieve (type-safe)
u := ctxkey.MustGet(ctx, userKey)           // zero value if missing
```

beauty's auth/requestid/callbacks/ratelimit/audit/afterwork/metadata/errors
all use this package, eliminating hand-written type assertions and key collision risk.

## Quick Reference: examples/clan (Compose Guild from Existing Primitives, No New Package)

Proves `pkg/` primitive composition already covers guild scenarios, no new package needed:
- `relationship` for members and roles (leader=StateOwner / member=StateActive);
- `tournament` for guild war seasonal leaderboard (cron reset + ranking);
- `wallet` for guild fund (donate/distribute);
- `party` for in-guild squads (temporary teams).

Routes: `/create` `/join` `/members` `/donate` `/fund` `/score` `/ranking`. See `examples/clan`.

---

# Extended Primitives Quick Reference (Concurrency / Reliability / Games & Live Streaming / Spatial Geography)

## Quick Reference: pkg/store/idempotency (Idempotent Execution)

Per-key dedup + concurrent merge (singleflight) in one: same key repeated executes once; result cached by TTL.

```go
store := idempotency.New[int64](idempotency.WithTTL(10 * time.Minute))
defer store.Stop()
val, err, shared := store.Do("order:"+id, func() (int64, error) {
    return chargeAndGrant(id) // executes once; concurrent same key blocks waiting for first result
})
// shared=true means reused another's result (fn not actually executed)
```

- Default **does not cache errors** (allows retry); `WithCacheErrors(true)` caches errors too;
- `fn` panic clears placeholder, allows retry;
- Idempotency key must be **stable** (from business/message ID), not generated on the spot with `idgen`/`uuid`. See `examples/idempotency`.

## Quick Reference: pkg/foundation/keyedmutex (Fine-Grained Per-Key Locks)

Same key serial, different keys parallel. Ref count zero auto-reclaims lock, no leak as keys grow.

```go
km := keyedmutex.New()
unlock := km.Lock("acc:"+id)   // only mutex with same account
defer unlock()
// ... critical section (same account debit serial, different accounts parallel) ...

if u, ok := km.TryLock(k); ok { defer u() }  // non-blocking try
km.Do(k, func() { ... })                     // convenience wrapper
```

- `Lock` returns `unlock` closure (not `Unlock(key)`), `sync.Once` prevents double unlock;
- Distinguish from `idempotency`: latter "execute once"; this "execute every time, just serially". See `examples/keyedmutex`.

## Quick Reference: pkg/resilience/backoff (Exponential Backoff + Jitter)

Unified backoff strategy: `Duration(n)` computes nth wait; `Retry`/`RetryIf` wraps retriable operations.

```go
p := backoff.New(
    backoff.WithBase(200*time.Millisecond), backoff.WithFactor(2),
    backoff.WithMax(30*time.Second), backoff.WithJitter(backoff.JitterFull),
)
err := p.RetryIf(ctx, callRemote, func(e error) bool {
    return !errors.Is(e, errBadRequest) // don't retry 4xx
})
```

- Four jitter modes: `JitterFull` (default, most spread) / `Equal` / `None` / `Proportional` (±ratio, default ±25%);
- `Retry` returns immediately on ctx cancel; reused by webhook/saga/grpcclient. See `examples/backoff`.

## Quick Reference: pkg/orchestration/saga (Cross-Service Saga Orchestration)

Execute forward operations in order; on any failure, compensate succeeded steps in reverse for eventual consistency.

```go
res := saga.New("purchase", saga.WithCompensationRetry(3, 100*time.Millisecond)).
    Step("deduct", deductFn, refundFn).   // forward + compensation (must be idempotent)
    Step("grant", grantFn, nil).          // nil = no compensation needed
    Execute(ctx)
switch res.Status {
case saga.StatusCommitted:          /* success */
case saga.StatusCompensated:        /* failed but compensated, data consistent */
case saga.StatusCompensationFailed: /* compensation also failed, alert for manual intervention */
}
```

- Complements `txn` (in-process 2PC rollbackable): saga is cross-service compensation;
- Compensation must be idempotent (recommend pairing with `wallet.ApplyTx`); compensation phase uses `WithoutCancel`, unaffected by original ctx cancel;
- Pure in-memory, not persisted; crash recovery relies on re-deliverable trigger source. See `examples/saga`.

## Quick Reference: pkg/messaging/eventbus (In-Process Event Bus)

Subscribe by topic + callback dispatch; decouples "who sends" from "who receives".

```go
bus := eventbus.New[UserEvent]()
unsub := bus.Subscribe("user.login", func(topic string, e UserEvent) { ... })
defer unsub()
bus.Publish("user.login", UserEvent{UserID: "u1"}) // notify all subscribers for that topic
```

- Sync (default, `Publish` returns when processing done) or async (`WithAsync`); handler panic recovered via `pkg/foundation/safe`;
- Distinguish from `stream` (channel single-source fan-out, all subscribers same stream): eventbus is multi-topic, callback-style. See `examples/eventbus`.

## Quick Reference: pkg/orchestration/delayqueue (One-Shot Delayed Trigger)

Min-heap + single goroutine driver; runs callback at scheduled time; supports cancel/reschedule by key.

```go
q := delayqueue.New()
defer q.Stop()
q.Schedule("order:"+id, 15*time.Minute, cancelOrder) // cancel if unpaid after 15 min
q.Schedule("order:"+id, 30*time.Minute, cancelOrder) // same key Schedule again = reschedule (overwrite)
q.Cancel("order:"+id)                                // paid → cancel
```

- Fills the gap between `scheduler` (immediate) and `cron` (periodic): match countdown/buff expiry/timeout fallback;
- Callback runs in independent goroutine; panic recovered via `pkg/foundation/safe`. See `examples/delayqueue`.

## Quick Reference: pkg/resilience/counter (Sliding-Window Count / Quota)

Per-key time-window accumulation; `Allow` for in-window quota check.

```go
c := counter.New(time.Minute)   // 1-minute sliding window
defer c.Stop()
c.Incr("room:1:danmaku", 1)
if !c.Allow("user:"+uid, 1, 60) { /* over 60 in 1 minute, reject */ }
```

- Ring buckets + sharded locks; complements `ratelimit`: ratelimit controls **rate** (token bucket); counter controls **total within window**;
- Idle keys reclaimed by gc. See `examples/counter`.

## Quick Reference: pkg/store/tally (High-Frequency Cumulative Aggregation + Batch Flush)

Many small +1s merged in memory; periodic/threshold batch handed to flush, flattening write amplification.

```go
t := tally.New(func(ctx context.Context, batch map[string]int64) {
    batchWriteDB(batch) // N Adds trigger few flushes
}, tally.WithFlushInterval(time.Second))
defer t.Stop()          // Stop does final flush, no tail loss
t.Add("room:1:like", 1) // hot path, in-memory accumulate only
```

- Generic numeric types; complements `wallet` (per-entry precise ledger): tally is aggregatable, tail-loss-tolerant counting (likes/popularity);
- `flush` panic recovered via `pkg/foundation/safe`, doesn't affect subsequent. See `examples/tally`.

## Quick Reference: pkg/idgen (Distributed Unique ID / Snowflake)

64-bit trend-increasing ID: 41 timestamp + 10 node + 12 sequence.

```go
g, _ := idgen.New(1) // node ID 0..1023, unique per instance in deployment
id := g.MustNext()   // trend-increasing, globally unique
ts, node, seq := idgen.Parse(id)
```

- Epoch configurable (`WithEpoch`, immutable after launch); handles **clock rollback** (spin within tolerance, error beyond threshold, never silently duplicate);
- Complements `uuid` (128-bit string): idgen compact, sortable, suitable for primary keys/match IDs. See `examples/idgen`.

## Quick Reference: pkg/foundation/fsm (Generic Finite State Machine)

Declarative transition table; illegal transitions error instead of silent state change; Enter/Leave/Transition hooks.

```go
m := fsm.NewBuilder[State, Event](Waiting).
    Allow(Waiting, Start, Playing).
    Allow(Playing, Finish, Settled).
    OnEnter(func(to State, e Event) error { return nil }).
    Build()
_, err := m.Fire(Start)      // illegal transition returns ErrInvalidTransition, state unchanged
m.Can(Finish); m.Current()
```

- S/E are comparable enums; hook error can veto transition (OnLeave/OnTransition); concurrency-safe.
- Match/room/order state flow, prevent illegal jumps. See `examples/fsm`.

## Quick Reference: pkg/game/versus (Timed Multi-Party Competitive Scoring / Live PK)

Combines `fsm` (state) + `stream` (event stream) + countdown; two/multi-party timed competition, winner at deadline.

```go
m := versus.New("pk-1", []string{"A", "B"},
    versus.WithDuration(5*time.Minute),
    versus.WithOnEnd(func(r versus.Result) { /* win/loss/draw */ }))
m.Start()
m.Add("A", 100)                 // gift-converted score
ch, unsub := m.Subscribe(ctx)   // subscribe to score change events (→ SSE/WS)
```

- pending→running→ended state machine; ended idempotent; auto settle at deadline or manual `Finish`;
- Event stream internally reuses `stream.Broadcaster`. See `examples/versus` and `examples/live-pk` (multi-room composition).

## Quick Reference: pkg/game/momentum (Combo + Heat Time Decay)

Increment within combo window / reset on break; heat decays exponentially by half-life (lazy, no background goroutine).

```go
tr := momentum.New(momentum.WithComboWindow(2*time.Second), momentum.WithHalfLife(30*time.Second))
st := tr.Hit("room:1", 10)  // st.Combo combo count, st.Value current heat, st.MaxCombo all-time max combo
tr.Value("room:1")          // read with elapsed-time decay applied
tr.GC(1e-3)                 // reclaim cooled keys on demand
```

- Distinguish from `counter`/`leaderboard` (no decay): momentum reflects "how hot right now";
- Live combo effects, real-time heat leaderboard. See `examples/momentum`.

## Quick Reference: pkg/game/pathfind (Grid A* Pathfinding)

Shortest path on grid map; supports obstacles, movement cost, diagonal (can forbid corner-cutting).

```go
g := pathfind.NewGrid(w, h)
g.SetBlocked(pathfind.Point{X: 5, Y: 3}, true)
g.SetCost(pathfind.Point{X: 2, Y: 2}, 5) // swamp harder to traverse
path := g.FindPath(from, to, pathfind.WithDiagonal(true))
```

- Octile heuristic guarantees optimal; pure computation, same Grid can `FindPath` concurrently;
- Tower defense/SLG/click-to-move/monster chase. See `examples/pathfind`.

## Quick Reference: pkg/game/spatial (Grid Spatial Index / Nearby People)

Entities bucketed by coordinates; `Nearby`/`KNN` only scan neighbor cells + precise distance filter, avoiding full scan.

```go
ix := spatial.New[string](100) // cellSize≈typical query radius
ix.Add("alice", 10, 10); ix.Move("alice", 20, 15); ix.Remove("bob")
near := ix.Nearby(0, 0, 50, "me")   // within radius, ascending distance, exclude self
top := ix.KNN(0, 0, 5, 500)          // nearest 5
```

- Planar float64 coordinates (game maps); complements `geohash` (Earth lat/lng LBS);
- Benefit scales with size: small-radius query cost depends on **local density** not total N. Benchmark (uniform, radius 50):
  ~same as full scan at 10k entities (map overhead ~ offsets candidate reduction), grid ~10µs vs full ~171µs at 250k (~17×).
  Full scan simpler when entity count is small;
- Nearby people/MMO AOI/large-map partitioning. See `examples/spatial`.
- **Incremental sync** layers `spatial/aoi` diff + `pkg/game/replicate.Projector` on `spatial.Nearby` egress — see `examples/statesync`.

## Quick Reference: pkg/game/spatial/aoi + pkg/game/replicate (AOI incremental sync)

`aoi.Set` diffs the previous visibility set into enter/leave/stay; `replicate.Projector` combines DirtySet to produce per-viewer `Delta` (spawn/update/despawn/baseline).

```go
dirty, removed := dirtySet.Consume()
delta := projector.Project(frame, viewer, visible, dirty, removed, lookup)
track.RecordSent(delta) // after unreliable send
batch := track.OnAck(ack) // reliable CatchUp; check batch.Truncated
```

- `Journal` + `ViewerTrack` + `Ack` / `CatchUpBatch` recover from loss; client sends `resync` when `truncated=true`;
- See `examples/statesync`, `examples/statesync-quic`; Agones hosting: `examples/agones-room` + `examples/matchmaker-room` + `contrib/agones`.

## Quick Reference: pkg/game/snapbuf + inputclock + lagcomp (lag compensation)

| Package | Role |
|---|---|
| `snapbuf.Ring` | Recent N-frame world snapshots |
| `inputclock.Clock` | Maps (player, clientFrame)→serverFrame + RTT |
| `lagcomp.Compensator` | `WorldAt(shooter, clientFrame)` compensated lookup |

```go
clock.Record(inputclock.Sample{Player: p, ClientFrame: cf, ServerFrame: frame, ReceivedAt: time.Now()})
ring.Push(frame, worldSnapshot())
snap, atFrame, ok := comp.WorldAt(shooter, clientFrame)
```

- `gameloop.PushInput` carries `ClientFrame`; client prediction/rollback stays in application code.

## Quick Reference: pkg/game/gameroom + contrib/agones (room orchestration)

`gameroom.Manager` FSM: `Waiting → Ready → Running → Draining → Closed`, with Join/Leave, `ScheduleStart`, `Drain`.

```go
mgr := gameroom.New(gameroom.WithHooks(gameroom.Hooks{
    OnRunning: func(ctx context.Context, roomID string) error { /* start tick */ return nil },
}))
handle, _ := agones.AllocateRoom(mgr, gameroom.Spec{ID: "gs-1", MaxPlayers: 16})
ctrl, _ := handle.Attach(agonesSDK)
_ = ctrl.Run(ctx) // Watcher: Agones Shutdown → Drain → SDK.Shutdown
```

- One room per Pod on K8s; matchmaker `Allocator` returns address then clients dial WS. See `examples/agones-room`; local match→assign: `examples/matchmaker-room`; gRPC: `contrib/agones` `NewGRPCAllocator`.

## Quick Reference: pkg/game/geohash (Lat/Lng Geocoding)

Encode lat/lng to base32 string; same prefix means geographically adjacent — "nearby" reduces to string prefix lookup.

```go
h := geohash.Encode(39.9042, 116.4074, 8)      // "wx4g0bm6"
cover := geohash.CoverNeighbors(lat, lng, 6)   // center+8 neighbors prefix set (covers boundary gaps)
d := geohash.Distance(lat1, lng1, lat2, lng2)  // Haversine meters
```

- Nearby search: lookup by `CoverNeighbors` prefix set in DB/Redis, then filter precisely with `Distance`;
- Complements `spatial` (planar grid), for real Earth coordinate LBS. See `examples/geohash`.

## Quick Reference: pkg/game/loot (Weighted Random Draw / Gacha)

Draw by weight; Alias Method O(1) per draw after table build; optional pity and without-replacement draw.

```go
tb, _ := loot.NewTable([]loot.Item[string]{
    {Value: "common", Weight: 943, Rarity: 1},
    {Value: "epic", Weight: 7, Rarity: 5},
})
tb.Draw()                       // O(1) weighted draw one
tb.DrawDistinct(3)              // draw 3 without replacement (10-pull dedup)
p := loot.NewPuller(tb, 90, 5)  // force Rarity>=5 if 90 consecutive pulls without
it, pity := p.Draw()            // pity=true means this draw triggered pity
```

- Alias table read-only after build, concurrency-safe; `WithRand` injects reproducible RNG;
- `Puller` has pity counter state, not concurrency-safe (one per player). See `examples/loot`.

## Quick Reference: pkg/foundation/semaphore (Weighted Semaphore / Bulkhead Isolation)

Limits max concurrent occupancy of shared resources. Supports weighted acquire (heavy ops take more, light ops take less);
equal-weight scenario (bulkhead) is cost=1 special case.

```go
s := semaphore.New(
    semaphore.WithCapacity(20),                   // total capacity 20
    semaphore.WithMaxWait(100*time.Millisecond),  // wait up to 100ms when full
    semaphore.WithOnReject(func() { metrics.Incr("sem.reject") }),
)

// Equal-weight mode (bulkhead): cost=1 each time
err := s.Do(ctx, func() error { return callLight() })

// Weighted mode: heavy op takes 5 slots
err = s.DoWithCost(ctx, 5, func() error { return callHeavy() })

// Low-level API
s.Acquire(ctx, 3)   // manual acquire
s.Release(3)        // manual release
s.TryAcquire(2)     // non-blocking try

s.InFlight()        // current occupancy
s.Available()       // remaining capacity
s.Capacity()        // total capacity
```

**Typical scenarios**:

1. **Downstream API concurrency protection** — limit max concurrency to slow APIs, prevent goroutine flood:
   ```go
   var apiSem = semaphore.New(semaphore.WithCapacity(20))
   func CallSlowAPI(ctx context.Context) error {
       return apiSem.Do(ctx, func() error { return http.Post(...) })
   }
   ```

2. **Weighted DB connection isolation** — heavy queries take more, light queries take less, shared pool:
   ```go
   var dbSem = semaphore.New(semaphore.WithCapacity(100))
   func HeavyQuery(ctx context.Context) error {
       return dbSem.DoWithCost(ctx, 10, func() error { return db.ExecHeavy(ctx) })
   }
   ```

3. **Batch download concurrency limit** — 100 URLs but max 8 concurrent network connections:
   ```go
   var dlSem = semaphore.New(semaphore.WithCapacity(8))
   for _, u := range urls {
       go func(url string) { dlSem.Do(ctx, func() error { return download(ctx, url) }) }(u)
   }
   ```

4. **Cross-function hold** — `Acquire` in Open, `Release` in Close:
   ```go
   func OpenStream(ctx context.Context) (*Stream, error) {
       if err := streamSem.Acquire(ctx, 1); err != nil { return nil, err }
       return &Stream{}, nil
   }
   func (s *Stream) Close() { streamSem.Release(1) }
   ```

- Complements `ratelimit` (rate/how many per second): semaphore limits **simultaneous occupancy**;
- Pair with `circuitbreaker`: limit concurrency + trip on high error rate;
- Pair with `timeout`: limit concurrency + per-call timeout protection;
- `MaxWait=0` reject immediately when full (default); `MaxWait>0` queue wait, still reject on timeout;
- `Do`/`DoWithCost` auto acquire+release; context cancel aware; concurrency-safe.

## Quick Reference: pkg/resilience/throttle (Batch Aggregation Trigger)

Flush when N items accumulated or T time elapsed. Standard for log/event/DB batch writes.

```go
th := throttle.New[Event](func(batch []Event) {
    db.BulkInsert(batch)
}, throttle.WithMaxBatch(200), throttle.WithInterval(time.Second))

th.Start(ctx)
defer th.Stop() // stop and flush remainder

th.Add(event)           // auto flush at 200
th.AddBatch(events)     // batch add
th.Flush()              // manual trigger
th.Len()                // current buffer size
```

- `Add` flush immediately when full (caller goroutine runs flushFn); timer also triggers flush;
- `Stop` guarantees no data loss; context cancel still flushes on Stop;
- Concurrency-safe.

## Quick Reference: pkg/foundation/priority (Priority Queue)

Generic binary heap; supports Push/Pop/Peek/Update/Remove. Two variants: zero-overhead non-concurrent + Mutex concurrent-safe.

```go
// Min-heap (scheduling: earliest deadline at top)
q := priority.New[Task](func(a, b Task) bool { return a.Deadline.Before(b.Deadline) })
q.Push(task1)
q.Push(task2)
next := q.Peek()       // peek without dequeue
done := q.Pop()        // dequeue
q.Update(idx)          // element priority changed, re-heapify
q.Remove(idx)          // delete by index

// Concurrent-safe version
sq := priority.NewSync[int](func(a, b int) bool { return a < b })
sq.Push(3); sq.Push(1)
v, ok := sq.Pop()      // (1, true)
```

- `Queue[T]`: non-concurrent, zero lock overhead, for single goroutine / scheduler loop internal use;
- `SyncQueue[T]`: built-in Mutex, Pop/Peek returns (T, bool) for safe empty handling;
- `PushPop`: one operation Push+Pop, one less heap adjustment than separate calls.

## Quick Reference: pkg/resilience/timeout (Timeout Execution + Panic Recovery)

Add timeout + auto panic recovery + error classification (timeout/panic/business error, all `errors.Is`-able) to any function.

```go
// Basic: timeout + panic protection
err := timeout.Do(ctx, 3*time.Second, func(ctx context.Context) error {
    return callUntrusted(ctx)
})
if errors.Is(err, timeout.ErrTimeout) { /* timeout */ }
if errors.Is(err, timeout.ErrPanic)   { /* fn panicked */ }

// Generic: with return value
val, err := timeout.DoValue(ctx, time.Second, func(ctx context.Context) (Result, error) {
    return fetchData(ctx)
})
```

- Beyond bare `context.WithTimeout`: panic recovery (doesn't crash caller) + error classification;
- fn runs in independent goroutine; caller returns immediately on timeout (fn should check ctx to exit);
- Stateless, concurrency-safe.

## Quick Reference: pkg/foundation/pipeline (Multi-Stage Pipeline)

Type-safe Stage chain: each stage can have concurrent workers + bounded channel backpressure + fan-in/fan-out.

```go
src := pipeline.Source(ctx, urls)

// Stage 1: download (4 concurrent)
s1 := pipeline.Stage[string, []byte]{
    Process: func(ctx context.Context, url string, emit func([]byte)) error {
        data, err := download(ctx, url)
        if err != nil { return err }
        emit(data)
        return nil
    },
    Workers: 4, BufSize: 10,
}

// Stage 2: process
s2 := pipeline.Stage[[]byte, Result]{
    Process: func(ctx context.Context, data []byte, emit func(Result)) error {
        emit(process(data))
        return nil
    },
    Workers: 2,
}

mid, _ := pipeline.Pipe(ctx, src, s1)
out, _ := pipeline.Pipe(ctx, mid, s2)
for result := range out { ... }

// Utilities
pipeline.Merge(ctx, ch1, ch2, ch3) // fan-in: merge channels
pipeline.Split(ctx, input, 3)      // fan-out: round-robin to N paths
pipeline.Run(ctx, src, stage)      // single-stage shortcut, collect results
```

- Distinguish from `stream` (broadcast/fan-out): pipeline is sequential stage chain, data flows through multiple steps;
- Any stage error terminates entire pipeline; context cancel stops whole chain;
- emit can output 0~N items (filter/expand); generics guarantee type safety between stages.

## Quick Reference: pkg/resilience/circuitbreaker (Circuit Breaker)

Three-state protection: Closed (normal) → Open (fast fail) → HalfOpen (probe) → Closed/Open.
Sliding window tracks error rate; auto trip above threshold; after cooldown, allow limited probe requests to verify recovery.

```go
cb := circuitbreaker.New(
    circuitbreaker.WithThreshold(0.5),    // 50% error rate triggers
    circuitbreaker.WithWindow(10*time.Second),
    circuitbreaker.WithCooldown(5*time.Second),
    circuitbreaker.WithHalfOpenMax(3),
    circuitbreaker.WithMinRequests(10),
    circuitbreaker.WithOnStateChange(func(from, to circuitbreaker.State) {
        log.Printf("breaker: %s -> %s", from, to)
    }),
)

err := cb.Do(func() error {
    return callDownstream()
})
if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
    // fallback logic
}
```

- Complements `backoff` (retry)/`ratelimit` (rate limit)/`cooldown` (cooldown) as resilience quartet;
- `Do` wraps one call: Closed executes and counts; Open immediately `ErrCircuitOpen`; HalfOpen probes;
- Concurrency-safe (single lock). See `pkg/resilience/circuitbreaker`.

## Quick Reference: pkg/resilience/cooldown (Cooldown / Action Timing)

Per-key "next available time"; can only trigger again after that point.

```go
cd := cooldown.New(8 * time.Second) // default CD
defer cd.Stop()
if cd.TryTrigger("p1:skill") {      // atomic "check+trigger": ready → trigger, return true
    castSkill()
}
cd.Remaining("p1:skill")            // remaining CD
cd.TriggerFor("p1:daily", 24*time.Hour) // per-action override default CD
```

- Distinguish from `ratelimit` (rate)/`counter` (window total): cooldown controls **minimum interval between two actions**;
- Sharded locks + gc reclaims idle keys. See `examples/cooldown`.

## Quick Reference: pkg/foundation/ringbuffer (Fixed-Length Ring Buffer / Recent N)

Keep only recent N; overwrite oldest when full, O(1) append.

```go
r := ringbuffer.New[string](50)   // recent 50
r.Push("danmaku")
r.Recent(10)                       // recent 10 (new to old)
r.Slice()                          // all (old to new)
s := ringbuffer.NewSync[string](50) // concurrent-safe variant
```

- `Ring[T]` non-concurrent (zero overhead), `SyncRing[T]` built-in RWMutex;
- Fixed memory, no expansion. Recent danmaku/match history/rolling logs. See `examples/ringbuffer`.

## Quick Reference: pkg/foundation/bitmap (Bitmap / Check-in)

1 bit per boolean state; large-scale marking + set operations, extremely memory-efficient.

```go
day := bitmap.New(1e7)             // 10M users, ~1.25MB per day
day.Set(uid); day.Test(uid); day.Count()   // today's check-in count
mon.Clone().And(tue).And(wed)      // checked in all three days (intersection)
bitmap.ConsecutiveFromEnd(days, uid)       // consecutive check-in days from end
```

- Exact; distinguish from `pkg/utils/bloom` (probabilistic, false positives): use bitmap when IDs dense, need exact Count/enumeration;
- Underlying `[]uint64` grows on demand, non-concurrent. Check-in/dedup/permission bits. See `examples/bitmap`.

## Quick Reference: pkg/domain/energy (Stamina / Energy System)

Lazy time regeneration + spend + recharge + countdown. No ticker dependency; O(1) replenish on call.

```go
e := energy.New(
    energy.WithCap(100),
    energy.WithRegenInterval(5*time.Minute),
    energy.WithRegenAmount(1),
    energy.WithOverflow(false),
)

e.Spend(30)                      // spend 30; insufficient returns ErrInsufficient
e.Add(50)                        // recharge (overflow=false caps at max)
e.Current()                      // current value (lazy replenish)
e.TimeToFull()                   // time until full
e.TimeToAmount(80)               // time until reaching 80
cur, ts := e.Snapshot()          // persistence snapshot
e2 := energy.NewWithState(cur, ts, ...) // restore from DB
```

- Distinguish from `wallet` (precise ledger): energy is "time auto-regen + spend" specialized resource;
- Concurrency-safe (single lock); `Snapshot`/`NewWithState` support persistence restore.

## Quick Reference: pkg/domain/signin (Daily Check-in)

Monthly bitmap stores check-ins + consecutive days + retro check-in limit + reward callback.

```go
r := signin.New(
    signin.WithRetroMax(3),
    signin.WithLocation(time.Local),
    signin.WithRewardFunc(func(day, streak int) []signin.Reward {
        return []signin.Reward{{Type: "coin", Amount: streak * 10}}
    }),
)

rewards, _ := r.SignIn(time.Now())    // today's check-in (idempotent)
r.Streak()                            // current consecutive days
r.SignedDays(2026, 7)                 // days signed in July 2026
r.MonthBitmap(2026, 7)                // bitmap (bit0=1st)
r.RetroSign(time.Now(), 2026, 7, 5)  // retro check-in July 5
r.RetroRemaining(time.Now())          // remaining retro check-ins this month
```

- Distinguish from `questlog` (goal progress + claim): signin is "calendar check-in + consecutive days";
- Retro check-in auto-recalculates streak; bitmap zero-dependency embedded (no `pkg/foundation/bitmap` dependency);
- Concurrency-safe.

## Quick Reference: pkg/domain/mail (In-Game Mailbox)

Generic attachments + claim state (unread→read→claimed) + expiry + batch send + Store interface.

```go
store := mail.NewMemoryStore[Attachment]()  // dev use; production uses DB Store
mb := mail.NewMailbox[Attachment](store,
    mail.WithMaxPerUser(100),
    mail.WithDefaultTTL(30*24*time.Hour),
    mail.WithOnClaim(func(id, recipient string) { /* reward analytics */ }),
)

mb.Send(&mail.Mail[Attachment]{ID: "m1", RecipientID: "p1", ...})
mb.BatchSend(playerIDs, template, idGen)   // server-wide batch send
mb.List("p1", mail.Filter{Limit: 20})      // fetch (auto filter expired, unread first)
mb.Read("m1")                              // mark read
att, _ := mb.Claim("m1")                   // claim attachment (once only)
mb.Unread("p1")                            // unread count (badge)
mb.DeleteExpired()                         // clean expired mail
```

- Distinguish from `notification` (ephemeral push/offline retention): mail has attachments + claim state machine + expiry;
- Distinguish from `inbox` (P2P text messages): mail has generic attachments;
- `Store[T]` interface: Save/Get/Update/Delete/List/CountByStatus/DeleteExpired;
- Concurrency-safe (depends on Store implementation).

## Quick Reference: pkg/game/questlog (Quest / Achievement Progress)

Accumulate progress toward goals; claim once when met; supports prerequisites and period reset.

```go
log := questlog.New([]questlog.Quest[string]{
    {ID: "kill", Target: 10},
    {ID: "vip", Target: 1, Requires: []string{"kill"}}, // unlock after prerequisite claimed
}, questlog.WithOnClaim(func(owner string, q questlog.Quest[string]) { grant(owner, q) }))

log.Advance("u1", "kill", 3)   // accumulate progress (auto Achieved when met)
log.Claim("u1", "kill")        // only Achieved can claim, idempotent
log.Claimable("u1")            // claimable list (badge use)
log.Reset("u1", "kill")        // refresh periodic quest
```

- Four states: Locked (prerequisite incomplete) → InProgress → Achieved → Claimed;
- Distinguish from counter (window count, expires): questlog is "accumulate toward goal + claim state machine", progress doesn't decrease over time. See `examples/questlog`.

## Quick Reference: pkg/game/leveling (Experience / Level Curve)

Add experience to current total; compute new level/levels gained/progress within level. Pure computation, stateless.

```go
lv := leveling.New(leveling.Poly(100, 2, 30)) // quadratic curve, max level 30
r := lv.Gain(totalExp, 80)   // add 80 exp
// r.Level / r.LeveledUp / r.LevelsGain / r.CurExp / r.NextExp / r.IsMax
lv.Stat(totalExp)            // read-only (display "exp until next level")
```

- Three curves: `Linear` (arithmetic)/`Poly` (polynomial acceleration)/`Table` (lookup, for designer numbers);
- After max level exp still accumulates but level doesn't increase; exp persisted by caller, this package does pure conversion. See `examples/leveling`.

## Quick Reference: pkg/game/reddot (Badge / Unread Aggregation Tree)

Set unread on leaves; parent = sum of descendants; clear propagates upward.

```go
tr := reddot.New()
tr.Set("me/msg/chat", 3)      // set unread on leaf
tr.Incr("me/friend/req", 1)
tr.Count("me")                // aggregate unread (sum of all descendants)
tr.Dot("me/msg")              // show badge (boolean)
tr.Children("me")             // aggregate unread per child category (render list)
tr.Clear("me/msg")            // mark category read, badge updates up parent chain
```

- Path-style nodes ("me/msg/chat"), tree lazily created; Count (exact "99+") vs Dot (boolean) two semantics;
- Concurrency-safe (badge tree small, single lock). App "Me" page badge aggregation. See `examples/reddot`.

## Production Multi-Instance: In-Memory vs Store Backend

These primitives default to **in-memory, single-process**: state doesn't cross instances; lost on restart. Split into three tiers by state nature to decide multi-instance production readiness:

**① Stateless / Pure Computation — Ready to Use**
`idgen` (node ID must be unique per instance), `backoff`, `geohash`, `pathfind`, `leveling`, `fsm`. No cross-request shared state; multi-instance and restart have no impact.

**② State Naturally Belongs to Single Process / Single Match — Scenario-Dependent**
`loot` (read-only table), `ringbuffer`, `bitmap`, `spatial`, `momentum`, `versus` (single match), `keyedmutex`/`eventbus` (in-process semantics), `delayqueue`/`saga` (recovery via MQ re-delivery). State is naturally "this machine/this match" local view or has independent recovery path.

**③ Cross-Instance Shared State — Upgrade with `WithStore`**
`counter` (quota), `cooldown` (cooldown), `idempotency` (dedup) will be wrong if each instance counts separately (quota bypassed, duplicate claim on instance switch, retry duplicate execution). These three support `WithStore(kvstore.Store)`: configure to store state in shared backend (Redis etc.), consistent across instances.

```go
store := myRedisStore          // implement pkg/store/kvstore.Store interface (one Redis command per method)
c := counter.New(time.Minute, counter.WithStore(store))     // quota cross-instance
cd := cooldown.New(8*time.Second, cooldown.WithStore(store)) // cooldown cross-instance
im := idempotency.New[T](idempotency.WithStore(store))       // dedup cross-instance
```

- Without `WithStore`, behavior and API unchanged (default in-memory, zero overhead);
- `pkg/store/kvstore` defines interface + in-memory impl (`NewMemory`); Redis etc. backends implemented by user (stdlib-only, no SDK import);
- **Semantic differences** to note: counter store mode uses **fixed window** (not sliding, boundary may allow 2× burst); idempotency store mode is **dedup reuse** not "global singleflight" (concurrent same key across instances may each execute once, idempotency guaranteed by unique result storage — so idempotency key requires business operation itself safely retriable); store failure always **fail-open** (read returns 0 / allow / degrade execute) + `WithOnStoreError` reporting.

See `examples/kvstore-shared` (two instances in single process sharing Store, demonstrates cross-instance quota/cooldown/dedup).

**④ Cross-Instance Mutex/Coordination — Use `pkg/store/dlock`**
`keyedmutex` is in-process lock; `saga`/`delayqueue` each bypass cross-process coordination via "result idempotency" or "MQ re-delivery";
but some scenarios need **cross-process mutex** itself — most typical is "only one instance should run Cron in multi-instance deployment".
These aren't solved by adding Store to a primitive (mutex/leader election is another dimension); need independent distributed lock/leader primitive, see next section.

## Quick Reference: pkg/store/dlock (Distributed Lock / Leader Election)

Backend-agnostic interface for cross-process mutex (`Locker`) and continuous leader election (`Elector`). Core use: in multi-instance deployment,
only one instance executes something (most typical: Cron scheduled tasks, see `pkg/service/cron.WithLeaderElector`).

```go
// Dev/test/single instance: in-memory (multi goroutine compete, semantically equivalent to "multi-instance compete")
elector := dlock.NewMemory()

// Production: based on etcd official concurrency package (Session+Mutex/Election), truly cross-process
client, _ := clientv3.New(clientv3.Config{Endpoints: []string{"etcd:2379"}})
elector := etcd.NewDLock(client, etcd.WithSessionTTL(10))

// Locker: one-shot mutex
lock, err := elector.Lock(ctx, "job:daily-report")
defer lock.Unlock(ctx)

// Elector: continuous campaign; run task while elected; stop immediately on losing leader (leaderCtx cancelled)
elector.Run(ctx, "myservice-cron", func(leaderCtx context.Context) {
    for {
        select {
        case <-leaderCtx.Done():
            return // lost leader, must stop work
        case <-time.After(time.Minute):
            doWork()
        }
    }
})
```

- `Run`'s `onElected` must continuously check `leaderCtx.Done()` — that's the only proof of "still leader";
- Two real backends, choose by environment:
  - `pkg/infra/etcd.DLock` — based on etcd official `client/v3/concurrency` (Session+Mutex/
    Election). Implements `Locker` (one-shot mutex) + `Elector` (leader election). Integration tests (need real
    etcd, `go test -tags=integration`) verify mutex between two independent client connections, 5-client leader
    uniqueness, failover after process crash;
  - `pkg/infra/k8s.Elector` — based on client-go `leaderelection` + `coordination.k8s.io/v1`
    Lease resource (same mechanism as kube-scheduler etc. control plane). Use when deployed in k8s without
    extra etcd ops; reuse cluster-native leader election. **Only implements `Elector`, not
    `Locker`** — client-go leaderelection semantics is "continuous campaign", no generic one-shot mutex
    primitive; reflects this limitation honestly rather than assembling semantically mismatched Lock/Unlock. Tests use client-go
    fake clientset (verify wiring and lifecycle callbacks; fake clientset doesn't do resourceVersion
    optimistic lock arbitration, can't verify real mutex — noted honestly in test comments, no "multiple Electors
    compete for same Lease" assertion — left for real cluster);
- Distinguish from `keyedmutex` (in-process): dlock is cross-process/cross-instance. See `examples/cron-leader`.

## Style Conventions

All packages follow unified conventions for easy mixing:

- **Stdlib only** — except `pkg/transport/ws/session` reuses `pkg/transport/ws` (depends on `coder/websocket`) and
  `pkg/domain/tournament` reuses `robfig/cron/v3` (cron parsing), all others zero third-party deps,
  can be copied to any Go project directly.
- **Namespace layering** — `pkg/` for generic primitives (session/presence/routing/ranking/scheduling/audit),
  `pkg/domain/` for business entities (account/notification/party/tournament/storage/relationship). Business entities carry
  concrete business semantics, grouped under `domain` for clarity and isolation.
- **Generics + functional Options** — `type Option func(*config)`, `config` unexported,
  defaults set in `New`.
- **Context-driven lifecycle** — `Start(ctx)` / `Stop()` / `Wait()`, following beauty's
  reverse graceful shutdown convention.
- **Concurrency-safe** — all exported types usable concurrently; backpressure always "full → drop/degrade" not block.
- **English package comments** — first line `// Package xxx ...`, describing scenario and design origin.

## Relationship to Existing Packages

| Existing Package | Relationship |
|--------|------|
| `pkg/transport/ws` | Underlying layer for `session`; can still use standalone when not using `session`'s `Handler` |
| `pkg/messaging/stream` | `Broadcaster` fan-out semantics enhanced by `router` (adds targeted/by-stream/batching) |
| `pkg/foundation/chanx` | Unbounded channel; `match`/`scheduler` internally use bounded channel + degrade as needed |
| `pkg/service/cron` | Complements `scheduler`: cron by expression, scheduler by event + pausable;
  `tournament` reuses its `robfig/cron` parsing for reset points |
| `pkg/foundation/xgo.Pool` | `beauty.Go` global pool; these packages' goroutines self-manage lifecycle via `Start/Stop` |

## References

- Demos: `examples/{match,session,presence,router,leaderboard,scheduler,matchmaker,
  audit,wallet,notification,tournament,party,storage,relationship,token,dberr,webhook,
  resume,status,chat,ephemeral,clan,afterwork,group,txn,loadbalance}/main.go`.
- Tests: each package `*_test.go`, all pass `go test -race -count=3`.
