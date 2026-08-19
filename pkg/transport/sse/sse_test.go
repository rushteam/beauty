package sse

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/middleware/compress"
)

func TestHandler_SetsHeadersAndFormatsEvents(t *testing.T) {
	h := Handler(func(_ *http.Request, sink Sink) error {
		_ = sink.Send(Event{Event: "greeting", Data: "hello"})
		_ = sink.Send(Event{ID: "42", Data: "line1\nline2", Retry: 3000})
		return nil
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("cache-control = %q", cc)
	}

	body := rec.Body.String()
	want := []string{
		"event: greeting\n",
		"data: hello\n",
		"id: 42\n",
		"retry: 3000\n",
		"data: line1\n",
		"data: line2\n",
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q\n--- body ---\n%s", w, body)
		}
	}
	// 每条事件应以空行结尾
	if !strings.Contains(body, "data: hello\n\n") {
		t.Errorf("event must end with a blank line\n%s", body)
	}
}

func TestHandler_Comment(t *testing.T) {
	h := Handler(func(_ *http.Request, sink Sink) error {
		return sink.Comment("keepalive")
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), ": keepalive\n\n") {
		t.Fatalf("comment not found: %q", rec.Body.String())
	}
}

func TestHandler_SanitizesInjection(t *testing.T) {
	h := Handler(func(_ *http.Request, sink Sink) error {
		// id 含换行不能破坏帧结构
		return sink.Send(Event{ID: "a\nid: evil", Data: "x"})
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	// 换行被去除，攻击者无法注入出独立的新字段行：整个 body 里行首 "id: " 只应出现一次。
	if c := strings.Count("\n"+body, "\nid: "); c != 1 {
		t.Fatalf("ID newline must be sanitized (want one id field line, got %d): %q", c, body)
	}
}

// 客户端断开（ctx 取消）时 handler 应能感知并结束。
func TestHandler_ClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h := Handler(func(r *http.Request, sink Sink) error {
		<-r.Context().Done()
		close(done)
		return ctx.Err()
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	go h.ServeHTTP(rec, req)
	cancel()
	<-done // 不阻塞即说明 handler 在 ctx 取消后返回
}

// handler 应能照常读取请求内容：query 参数与 Last-Event-ID 头（断点续传）。
func TestHandler_ReadsRequest(t *testing.T) {
	h := Handler(func(r *http.Request, sink Sink) error {
		topic := r.URL.Query().Get("topic")
		lastID := r.Header.Get("Last-Event-ID")
		return sink.Send(Event{Data: "topic=" + topic + ",last=" + lastID})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events?topic=orders", nil)
	req.Header.Set("Last-Event-ID", "100")
	h.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "data: topic=orders,last=100\n") {
		t.Fatalf("handler did not read request fields: %q", rec.Body.String())
	}
}

// Send 应并发安全（带锁）。
func TestSink_ConcurrentSend(t *testing.T) {
	h := Handler(func(_ *http.Request, sink Sink) error {
		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() {
				_ = sink.Send(Event{Data: "x"})
			})
		}
		wg.Wait()
		return nil
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if n := strings.Count(rec.Body.String(), "data: x\n"); n != 50 {
		t.Fatalf("want 50 events, got %d", n)
	}
}

// 经过 compress 中间件时，flush 仍应穿透包装链把数据下发并可解压。
func TestHandler_ThroughCompressMiddleware(t *testing.T) {
	var handler http.Handler = Handler(func(_ *http.Request, sink Sink) error {
		return sink.Send(Event{Data: "compressed-event"})
	})
	handler = compress.Middleware(1)(handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("want gzip, got %q", rec.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !strings.Contains(string(plain), "data: compressed-event\n") {
		t.Fatalf("event not found in decompressed stream: %q", string(plain))
	}
}
