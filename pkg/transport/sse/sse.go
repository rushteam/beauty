// Package sse 提供 Server-Sent Events 的轻量封装，屏蔽 SSE 在 Go 里的常见坑：
// 自动设置流式响应头、解除本连接的写超时（避免被 server WriteTimeout 掐断）、
// 每条事件后自动 flush（穿透 otelhttp / compress 等包装链）、客户端断开时结束。
package sse

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultWriteTimeout 是单次事件写入的默认截止时间。慢/死客户端导致单次
// 写入超过该时长即返回错误、结束流，避免 goroutine 被无限钉死。
// 它是“每次写入”而非“整条连接”的超时，每次 Send 都会重置，因此不会掐断长连接。
const DefaultWriteTimeout = 30 * time.Second

// 复用事件格式化缓冲，降低高频推送的分配。
var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// Event 是一条 Server-Sent Event。
type Event struct {
	// ID 事件 ID（可选）。设置后客户端断线重连会带上 Last-Event-ID，可用于断点续传。
	ID string
	// Event 事件类型（可选）。客户端可用 addEventListener(type) 订阅。
	Event string
	// Data 事件数据。可含换行，会被自动拆成多行 data: 字段。
	Data string
	// Retry 客户端断线后的重连等待（毫秒，可选，>0 才发送）。
	Retry int
}

// Sink 用于在 handler 中向客户端推送事件。并发安全。
type Sink interface {
	// Send 发送一条事件并立即 flush。客户端已断开时返回错误。
	Send(Event) error
	// Comment 发送一条注释行（": text"），客户端会忽略，常用于心跳保活。
	Comment(text string) error
}

// Option 配置 Handler。
type Option func(*config)

type config struct {
	writeTimeout time.Duration
}

// WithWriteTimeout 设置单次事件写入的截止时间，默认 DefaultWriteTimeout（30s）。
// 传 0 表示不限制（清除写超时；此时慢客户端可能长时间占用 goroutine，慎用）。
func WithWriteTimeout(d time.Duration) Option {
	return func(c *config) { c.writeTimeout = d }
}

// Handler 把 fn 包装成一个 SSE 的 http.HandlerFunc：
//   - 设置 Content-Type: text/event-stream 等响应头
//   - 每次写入设置滚动写超时（默认 30s，见 WithWriteTimeout），慢/死客户端不会钉死 goroutine
//   - 每条事件后自动 flush
//   - fn 返回或客户端断开（r.Context() 取消）时结束
//
// fn 接收完整的 *http.Request，可照常读取 query、header（如断线重连的
// Last-Event-ID）、path 通配符、body 等；取消信号通过 r.Context() 获取。
// fn 在请求 goroutine 中执行；若从其它 goroutine 推送，可安全并发调用 Sink（内部有锁）。
//
//	sse.Handler(func(r *http.Request, sink sse.Sink) error {
//	    topic := r.URL.Query().Get("topic")
//	    lastID := r.Header.Get("Last-Event-ID") // 断点续传
//	    ctx := r.Context()
//	    ...
//	})
func Handler(fn func(r *http.Request, sink Sink) error, opts ...Option) http.HandlerFunc {
	cfg := config{writeTimeout: DefaultWriteTimeout}
	for _, o := range opts {
		o(&cfg)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no") // 提示 nginx 等反代不要缓冲

		rc := http.NewResponseController(w)
		s := &sink{w: w, rc: rc, writeTimeout: cfg.writeTimeout}

		// 先发一个空 flush，确保响应头尽快下发、连接进入流式状态
		s.setDeadline()
		w.WriteHeader(http.StatusOK)
		_ = rc.Flush()

		if err := fn(r, s); err != nil && r.Context().Err() == nil {
			// fn 主动报错且非客户端断开：以注释形式告知（此时 header 已发出，无法再改状态码）
			_ = s.Comment("error: " + err.Error())
		}
	}
}

type sink struct {
	mu           sync.Mutex
	w            http.ResponseWriter
	rc           *http.ResponseController
	writeTimeout time.Duration
}

func (s *sink) Send(e Event) error {
	b := bufPool.Get().(*bytes.Buffer)
	b.Reset()
	defer bufPool.Put(b)

	if e.ID != "" {
		fmt.Fprintf(b, "id: %s\n", sanitize(e.ID))
	}
	if e.Event != "" {
		fmt.Fprintf(b, "event: %s\n", sanitize(e.Event))
	}
	if e.Retry > 0 {
		fmt.Fprintf(b, "retry: %d\n", e.Retry)
	}
	// data 可多行：每行单独一个 data: 字段
	for line := range strings.SplitSeq(e.Data, "\n") {
		fmt.Fprintf(b, "data: %s\n", strings.TrimSuffix(line, "\r"))
	}
	b.WriteByte('\n') // 事件以空行结束
	return s.write(b.Bytes())
}

func (s *sink) Comment(text string) error {
	return s.write([]byte(": " + sanitize(text) + "\n\n"))
}

// setDeadline 为下一次写入设置滚动截止时间（writeTimeout>0 时）。
// 调用方持有 s.mu（或在初始 flush 的串行阶段）。
func (s *sink) setDeadline() {
	if s.writeTimeout > 0 {
		_ = s.rc.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	} else {
		_ = s.rc.SetWriteDeadline(time.Time{}) // 显式清除，避免被外层 WriteTimeout 掐断
	}
}

func (s *sink) write(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setDeadline()
	if _, err := s.w.Write(payload); err != nil {
		return err
	}
	return s.rc.Flush()
}

// sanitize 去掉会破坏 SSE 帧结构的换行符（id/event/comment 等单行字段）。
func sanitize(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}
