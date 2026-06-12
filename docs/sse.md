# Server-Sent Events（pkg/sse）

`pkg/sse` 封装了 SSE（服务器推送）在 Go 里的常见坑：自动设置流式响应头、
解决写超时掐断、每条事件自动 flush（穿透 otelhttp / compress 包装链）、客户端断开自动结束。

## 快速开始

```go
import "github.com/rushteam/beauty/pkg/sse"

mux := http.NewServeMux()
mux.Handle("/events", sse.Handler(func(r *http.Request, sink sse.Sink) error {
    topic  := r.URL.Query().Get("topic")     // 照常读 query
    lastID := r.Header.Get("Last-Event-ID")  // 断线重连：续传起点
    ctx := r.Context()
    for {
        select {
        case <-ctx.Done():                    // 客户端断开自动结束
            return ctx.Err()
        case ev := <-subscribe(topic, lastID):
            if err := sink.Send(sse.Event{ID: ev.ID, Event: "msg", Data: ev.Data}); err != nil {
                return err                     // 写失败（连接断了）即停止
            }
        }
    }
}))

app := beauty.New(beauty.WithService(webserver.New(":8080", mux)))
```

## Event

```go
type Event struct {
    ID    string // 可选；客户端重连会带 Last-Event-ID，可用于断点续传
    Event string // 可选；客户端 addEventListener(type) 订阅
    Data  string // 数据；含换行会自动拆成多行 data: 字段
    Retry int    // 可选；客户端重连等待（毫秒，>0 才发送）
}
```

`Sink` 接口（并发安全，可从多个 goroutine 调用）：

- `Send(Event) error` —— 发送一条事件并立即 flush。
- `Comment(text string) error` —— 发送注释行（`: text`），客户端忽略，常用于心跳保活。

## 选项

| 选项 | 说明 |
|------|------|
| `WithWriteTimeout(d)` | 单次事件写入的截止时间，默认 `DefaultWriteTimeout`（30s）。这是**每次写入**超时（每条事件重置），不会掐断长连接，但慢/死客户端单次写卡住即报错退出，避免 goroutine 被钉死。传 `0` 关闭（慎用）。 |

## 行为细节

- 自动设置 `Content-Type: text/event-stream`、`Cache-Control: no-cache`、
  `X-Accel-Buffering: no`（提示 nginx 等反代不要缓冲）。
- `id` / `event` / 注释等单行字段会清洗换行，防止注入伪造帧。
- 事件格式化使用 `sync.Pool` 复用缓冲，降低高频推送分配。

## 与中间件 / server 配合

- **写超时**：webserver 默认已不设 `WriteTimeout`（长连接友好）；即使你用
  `webserver.WithWriteTimeout` 设了，本包的滚动写超时也能保证单连接的流式不被掐断。
- **compress**：可叠加，gzip 会逐事件 flush（需客户端 `Accept-Encoding: gzip`）；
  一般 SSE 路由不必压缩。
- **timeout 中间件**：❌ 不要套在 SSE 路由上——`http.TimeoutHandler` 会缓冲整个响应。
