# Server-Sent Events (pkg/sse)

`pkg/sse` wraps the common pitfalls of SSE (server push) in Go: it sets streaming response headers automatically,
avoids write-timeout cutoffs, flushes each event (piercing otelhttp / compress wrapper chains), and ends cleanly when the client disconnects.

## Quick Start

```go
import "github.com/rushteam/beauty/pkg/sse"

mux := http.NewServeMux()
mux.Handle("/events", sse.Handler(func(r *http.Request, sink sse.Sink) error {
    topic  := r.URL.Query().Get("topic")     // read query as usual
    lastID := r.Header.Get("Last-Event-ID")  // reconnect: resume from this point
    ctx := r.Context()
    for {
        select {
        case <-ctx.Done():                    // client disconnect → exit
            return ctx.Err()
        case ev := <-subscribe(topic, lastID):
            if err := sink.Send(sse.Event{ID: ev.ID, Event: "msg", Data: ev.Data}); err != nil {
                return err                     // write failed (connection gone) → stop
            }
        }
    }
}))

app := beauty.New(beauty.WithService(webserver.New(":8080", mux)))
```

## Event

```go
type Event struct {
    ID    string // optional; client sends Last-Event-ID on reconnect for resume
    Event string // optional; client addEventListener(type) subscription
    Data  string // payload; newlines are split into multiple data: lines
    Retry int    // optional; client reconnect wait (ms, sent only when >0)
}
```

`Sink` interface (concurrency-safe; callable from multiple goroutines):

- `Send(Event) error` — send one event and flush immediately.
- `Comment(text string) error` — send a comment line (`: text`); ignored by clients; useful for keepalive heartbeats.

## Options

| Option | Description |
|------|------|
| `WithWriteTimeout(d)` | Deadline per event write; default `DefaultWriteTimeout` (30s). This is a **per-write** timeout (reset for each event), not a long-connection cutoff; slow/dead clients that block a single write will error out and exit, avoiding stuck goroutines. Pass `0` to disable (use with caution). |

## Behavior Details

- Automatically sets `Content-Type: text/event-stream`, `Cache-Control: no-cache`,
  `X-Accel-Buffering: no` (hint nginx and similar proxies not to buffer).
- Single-line fields like `id` / `event` / comments are sanitized for newlines to prevent frame injection.
- Event formatting uses `sync.Pool` to reuse buffers and reduce allocations under high-frequency push.

## Working with Middleware / Server

- **Write timeout**: webserver does not set `WriteTimeout` by default (long-connection friendly); even if you set one via
  `webserver.WithWriteTimeout`, this package's rolling write timeout keeps streaming on a single connection alive.
- **compress**: can be stacked; gzip flushes per event (client must send `Accept-Encoding: gzip`);
  SSE routes usually do not need compression.
- **timeout middleware**: do not wrap SSE routes — `http.TimeoutHandler` buffers the entire response.
