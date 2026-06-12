# WebSocket（pkg/ws）

`pkg/ws` 基于 [`github.com/coder/websocket`](https://github.com/coder/websocket) 提供轻量封装：
自动完成握手升级、统一关闭语义，并把 `*http.Request` 透传给业务（便于读 query / header /
子协议 / 鉴权信息）。

## 快速开始

```go
import "github.com/rushteam/beauty/pkg/ws"

mux := http.NewServeMux()
mux.Handle("/ws", ws.Handler(func(r *http.Request, c *ws.Conn) error {
    ctx := r.Context()
    for {
        typ, data, err := c.Read(ctx)
        if err != nil {
            return err // 客户端关闭 / 出错即退出
        }
        if err := c.Write(ctx, typ, data); err != nil { // echo
            return err
        }
    }
}))

app := beauty.New(beauty.WithService(webserver.New(":8080", mux)))
```

## Conn

| 方法 | 说明 |
|------|------|
| `Read(ctx) (MessageType, []byte, error)` | 读一条消息 |
| `Write(ctx, typ, data)` / `WriteText(ctx, s)` / `WriteBinary(ctx, b)` | 写消息 |
| `ReadJSON(ctx, v)` / `WriteJSON(ctx, v)` | JSON 读写 |
| `Ping(ctx)` | 主动 ping 探活 |
| `Subprotocol()` | 协商出的子协议 |
| `Close(code, reason)` | 主动关闭 |
| `Raw()` | 底层 `*websocket.Conn`，用于未覆盖的高级用法 |

消息类型常量：`ws.Text` / `ws.Binary`。

## 选项

| 选项 | 说明 |
|------|------|
| `WithSubprotocols(...)` | 服务端支持的子协议，握手协商 |
| `WithOriginPatterns(...)` | 允许跨域连接的 origin 模式（默认仅同源） |
| `WithInsecureSkipVerify()` | 关闭 origin 校验（仅开发 / 可信内网） |
| `WithReadLimit(n)` | 单条消息读取上限（字节），`-1` 不限，默认库默认 32KiB |
| `WithPingInterval(d)` | 后台心跳：每隔 `d` ping 一次，失败即关连接，检测半开 TCP；`d<=0` 不启用 |

## 关闭语义

- `fn` 返回 `nil` → 正常关闭（`StatusNormalClosure`）。
- `fn` 返回 `error` → `StatusInternalError` 关闭。
- 握手失败时 `Accept` 已写出错误响应，`Handler` 直接返回。
- `defer CloseNow()` 兜底，确保连接最终关闭。

## 注意事项

- **WebSocket 升级依赖 `http.Hijacker`**。框架最外层的 otelhttp 会透传 Hijacker，可正常使用；
  但**不要给 WebSocket 路由套 compress 中间件**（其 ResponseWriter 不直接实现 Hijacker）。
- **心跳**：ping 的 pong 由 `Read` 流程处理，因此 `WithPingInterval` 对“持续 Read 的连接”最有效；
  只写不读的连接建议在 `fn` 内调用 `c.Raw().CloseRead(ctx)` 让后台处理控制帧。
- 升级后连接从 `http.Server` 脱管，server 的读写超时不再适用，长连接不会被掐断。
