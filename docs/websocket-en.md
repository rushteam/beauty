# WebSocket (pkg/ws)

`pkg/ws` provides a lightweight wrapper around [`github.com/coder/websocket`](https://github.com/coder/websocket):
it handles the upgrade handshake automatically, unifies close semantics, and passes `*http.Request` through to
application code (so you can read query params, headers, subprotocols, and auth info).

## Quick Start

```go
import "github.com/rushteam/beauty/pkg/ws"

mux := http.NewServeMux()
mux.Handle("/ws", ws.Handler(func(r *http.Request, c *ws.Conn) error {
    ctx := r.Context()
    for {
        typ, data, err := c.Read(ctx)
        if err != nil {
            return err // client closed / error → exit
        }
        if err := c.Write(ctx, typ, data); err != nil { // echo
            return err
        }
    }
}))

app := beauty.New(beauty.WithService(webserver.New(":8080", mux)))
```

## Conn

| Method | Description |
|------|------|
| `Read(ctx) (MessageType, []byte, error)` | Read one message |
| `Write(ctx, typ, data)` / `WriteText(ctx, s)` / `WriteBinary(ctx, b)` | Write a message |
| `ReadJSON(ctx, v)` / `WriteJSON(ctx, v)` | JSON read/write |
| `Ping(ctx)` | Active ping for liveness |
| `Subprotocol()` | Negotiated subprotocol |
| `Close(code, reason)` | Close actively |
| `Raw()` | Underlying `*websocket.Conn` for advanced use not covered here |

Message type constants: `ws.Text` / `ws.Binary`.

## Options

| Option | Description |
|------|------|
| `WithSubprotocols(...)` | Server-supported subprotocols for handshake negotiation |
| `WithOriginPatterns(...)` | Allowed origin patterns for cross-origin connections (same-origin only by default) |
| `WithInsecureSkipVerify()` | Disable origin validation (dev / trusted internal network only) |
| `WithReadLimit(n)` | Max bytes per message read; `-1` = unlimited; default library default is 32 KiB |
| `WithPingInterval(d)` | Background heartbeat: ping every `d`; close on failure to detect half-open TCP; `d<=0` disables |

## Close Semantics

- `fn` returns `nil` → normal close (`StatusNormalClosure`).
- `fn` returns `error` → close with `StatusInternalError`.
- On handshake failure, `Accept` has already written an error response; `Handler` returns immediately.
- `defer CloseNow()` as a safety net to ensure the connection is eventually closed.

## Notes

- **WebSocket upgrade requires `http.Hijacker`**. The framework's outer otelhttp layer passes Hijacker through, so it works;
  but **do not wrap WebSocket routes with the compress middleware** (its ResponseWriter does not implement Hijacker directly).
- **Heartbeat**: pong responses to ping are handled in the `Read` loop, so `WithPingInterval` works best on connections that keep reading;
  for write-only connections, call `c.Raw().CloseRead(ctx)` inside `fn` so the background goroutine can handle control frames.
- After upgrade, the connection is detached from `http.Server`; server read/write timeouts no longer apply, so long-lived connections are not cut off.
