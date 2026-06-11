// Package sse 提供 Server-Sent Events 的轻量封装，屏蔽 SSE 在 Go 里的常见坑：
// 自动设置流式响应头、解除本连接的写超时（避免被 server WriteTimeout 掐断）、
// 每条事件后自动 flush（穿透 otelhttp / compress 等包装链）、客户端断开时结束。
package sse

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

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

// Handler 把 fn 包装成一个 SSE 的 http.HandlerFunc：
//   - 设置 Content-Type: text/event-stream 等响应头
//   - 解除本连接写超时（穿透 otelhttp/compress 等包装链；不支持时忽略）
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
func Handler(fn func(r *http.Request, sink Sink) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no") // 提示 nginx 等反代不要缓冲

		rc := http.NewResponseController(w)
		// 解除写超时：未设时无影响；设了 WriteTimeout 时避免长连接被掐断。
		// 不支持该能力的 ResponseWriter 会返回错误，忽略即可。
		_ = rc.SetWriteDeadline(time.Time{})

		s := &sink{w: w, rc: rc}

		// 先发一个空 flush，确保响应头尽快下发、连接进入流式状态
		w.WriteHeader(http.StatusOK)
		_ = rc.Flush()

		if err := fn(r, s); err != nil && r.Context().Err() == nil {
			// fn 主动报错且非客户端断开：以注释形式告知（此时 header 已发出，无法再改状态码）
			_ = s.Comment("error: " + err.Error())
		}
	}
}

type sink struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *sink) Send(e Event) error {
	var b strings.Builder
	if e.ID != "" {
		fmt.Fprintf(&b, "id: %s\n", sanitize(e.ID))
	}
	if e.Event != "" {
		fmt.Fprintf(&b, "event: %s\n", sanitize(e.Event))
	}
	if e.Retry > 0 {
		fmt.Fprintf(&b, "retry: %d\n", e.Retry)
	}
	// data 可多行：每行单独一个 data: 字段
	for line := range strings.SplitSeq(e.Data, "\n") {
		fmt.Fprintf(&b, "data: %s\n", strings.TrimSuffix(line, "\r"))
	}
	b.WriteByte('\n') // 事件以空行结束
	return s.write(b.String())
}

func (s *sink) Comment(text string) error {
	return s.write(": " + sanitize(text) + "\n\n")
}

func (s *sink) write(payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write([]byte(payload)); err != nil {
		return err
	}
	return s.rc.Flush()
}

// sanitize 去掉会破坏 SSE 帧结构的换行符（id/event/comment 等单行字段）。
func sanitize(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}
