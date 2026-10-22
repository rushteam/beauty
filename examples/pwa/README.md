# pwa —— Service Worker / PWA 配套示例

演示 beauty 为 PWA 提供的服务端机制：

| 能力 | 包 |
|---|---|
| Cache-Control / SWR | `pkg/middleware/httpcache` |
| Idempotency-Key 幂等写 | `pkg/middleware/idempotencykey` |
| 离线批量回放 `/sync` | `pkg/api/offlinesync` |
| Web Push（VAPID） | `contrib/webpush` |

## 运行

```bash
cd examples/pwa
go run .
# 浏览器打开 http://localhost:8088
```

需 **localhost 或 HTTPS**，否则无法注册 Service Worker / 申请通知权限。

## 试什么

1. **读公开 API**：看响应 `Cache-Control: public, max-age=30, stale-while-revalidate=60`
2. **下单**：带 `Idempotency-Key`；连点两次，第二次应有 `X-Idempotency-Replayed: true`，`order_id` 不变
3. **订阅 Push → 发送 Push**：浏览器弹出通知（需授权）
4. **离线回放**：DevTools → Network → Offline，点下单会入队；恢复网络后 SW `sync` 或刷新后走 `/sync`

## 静态资源

- `static/manifest.webmanifest` — 可安装清单
- `static/sw.js` — App Shell + SWR + 离线队列
- `static/index.html` / `app.js` — 极简前端
