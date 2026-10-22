# PWA / Service Worker 服务端配套

beauty **不实现**浏览器里的 Service Worker，只提供让 PWA 可靠工作的服务端机制：缓存头、幂等写、离线批量回放、Web Push。

职责划分：

| 侧 | 职责 |
|----|------|
| 浏览器 / SW | 注册、Cache API、Background Sync、Push 展示、`manifest` |
| beauty | `Cache-Control`、`Idempotency-Key`、`/sync` 契约、VAPID 推送 |

完整可运行示例：[`examples/pwa`](../examples/pwa)。

## 组件一览

| 能力 | 包 | 模块 |
|------|-----|------|
| Cache-Control / SWR | `pkg/middleware/httpcache` | core |
| Idempotency-Key 幂等写 | `pkg/middleware/idempotencykey` | core |
| 离线批量回放 | `pkg/api/offlinesync` | core |
| Web Push（VAPID） | `contrib/webpush` | 独立 contrib |

## 1. HTTP 缓存策略 (`httpcache`)

按路径前缀写入 `Cache-Control`（含 `stale-while-revalidate`），方便 SW 做 Cache First / Network First / SWR。下游已设头则不覆盖。

```go
import "github.com/rushteam/beauty/pkg/middleware/httpcache"

h := httpcache.Middleware(httpcache.NoStore,
    httpcache.Rule{Prefix: "/static/", Policy: httpcache.HashedAsset},
    httpcache.Rule{Prefix: "/api/public/", Policy: httpcache.PublicShort},
)(mux)
```

预设：

| 预设 | 典型用途 |
|------|----------|
| `NoStore` | 写 API / 私有接口 |
| `AppShell` | HTML 壳（短 `max-age` + SWR） |
| `HashedAsset` | 带 hash 的静态资源（长缓存 + `immutable`） |
| `PublicShort` | 可公开短缓存的只读 API |

机制、不政策：只设响应头；是否缓存由浏览器 / SW 决定。

## 2. Idempotency-Key (`idempotencykey`)

写操作（POST/PUT/PATCH/DELETE）带稳定幂等键；网络重试或 SW 回放时复用首次成功响应，避免重复副作用。

约定：

- 请求头：`Idempotency-Key: <非空字符串>`
- 复用响应附加：`X-Idempotency-Replayed: true`
- **5xx 不缓存**（允许随后重试）
- `WithRequireKey(true)` 时缺键返回 400

```go
import "github.com/rushteam/beauty/pkg/middleware/idempotencykey"

mux.Handle("POST /api/orders", idempotencykey.Middleware()(orderHandler))
```

底层复用 `pkg/store/idempotency`；跨实例去重可给 store 配 Redis（见 [idempotency](idempotency.md)）。

## 3. 离线批量回放 (`offlinesync`)

客户端（SW）在恢复网络后把积压写操作批量 POST：

```http
POST /sync
Content-Type: application/json

{
  "ops": [
    {"id":"uuid-1","method":"POST","path":"/api/orders","body":{"sku":"tea"}}
  ]
}
```

响应：

```json
{"results":[{"id":"uuid-1","status":201,"replayed":false,"body":{"order_id":1}}]}
```

```go
import "github.com/rushteam/beauty/pkg/api/offlinesync"

mux.Handle("POST /sync", offlinesync.New(apiMux)) // apiMux 为业务路由
```

每条 `op.id` 作为幂等键；`Executor` 通常是业务 `ServeMux`。默认单批最多 50 条、结果缓存 24h（可用 `WithMaxOps` / `WithTTL` 调整）。

## 4. Web Push (`contrib/webpush`)

独立模块（含 `webpush-go`），不进入 core `go.mod`：

```bash
go get github.com/rushteam/beauty/contrib/webpush@latest
```

```go
keys, _ := webpush.GenerateVAPIDKeys()
store := webpush.NewMemStore()
client := webpush.NewClient(keys, "mailto:ops@example.com", webpush.WithStore(store))

mux.Handle("/push/vapid", webpush.VAPIDPublicHandler(client))
mux.Handle("/push/subscribe", webpush.SubscribeHandler(store, nil))
_ = client.SendToUser(ctx, "user-1", []byte(`{"title":"hi","body":"hello"}`))
```

- `Store` 接口可换 Redis/DB；`MemStore` 适合开发
- 订阅返回 410/404 时自动 `Delete` 失效 endpoint
- 与前台 WS/SSE 互补：前台实时通道，后台/杀进程用 Push

## 与 SW 的推荐分工

```text
GET  / /static/*     → SW cache-first；服务端 AppShell / HashedAsset
GET  /api/public/*   → SW SWR；服务端 PublicShort
POST /api/*          → 失败入 IndexedDB；带 Idempotency-Key
online / sync 事件   → POST /sync 批量回放
PushManager.subscribe → POST /push/subscribe
```

## 安全注意

- Push / SW 需 **HTTPS 或 localhost**
- `/sync` 与写 API 应挂鉴权（`pkg/middleware/auth`）；本包只提供幂等机制
- VAPID 私钥勿进前端；公钥经 `/push/vapid` 下发即可

## 相关文档

- [幂等原语](idempotency.md)
- [中间件 README](../pkg/middleware/README.md)
- [contrib/webpush](../contrib/webpush/README.md)
- [examples/pwa](../examples/pwa/README.md)
