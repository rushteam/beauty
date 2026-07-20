package session_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rushteam/beauty/pkg/ws"
	"github.com/rushteam/beauty/pkg/ws/session"
)

type echoHandler struct {
	opened    atomic.Int32
	closed    atomic.Int32
	gotMsg    atomic.Int32
	closeOnce sync.Once
	closeCh   chan struct{}
}

func newEcho() *echoHandler {
	return &echoHandler{closeCh: make(chan struct{})}
}

func (h *echoHandler) OnOpen(s *session.Session) error {
	h.opened.Add(1)
	return nil
}
func (h *echoHandler) OnMessage(s *session.Session, typ session.MessageType, data []byte) error {
	h.gotMsg.Add(1)
	s.Send(typ, data)
	return nil
}
func (h *echoHandler) OnClose(s *session.Session, reason string) {
	h.closed.Add(1)
	h.closeOnce.Do(func() { close(h.closeCh) })
}

func startServer(t *testing.T, h session.Handler, opts ...session.Option) *httptest.Server {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(session.Accept(h, opts...), ws.WithInsecureSkipVerify()))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func TestSession_Echo(t *testing.T) {
	h := newEcho()
	srv := startServer(t, h, session.WithPingPeriod(0)) // 禁 ping 加快测试
	c := dial(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" || typ != websocket.MessageText {
		t.Fatalf("got %v %s", typ, data)
	}
	if h.opened.Load() != 1 {
		t.Fatal("OnOpen not called")
	}

	// 关闭连接,等 OnClose。
	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-h.closeCh:
	case <-time.After(time.Second):
		t.Fatal("OnClose not called")
	}
	if h.gotMsg.Load() != 1 {
		t.Fatalf("gotMsg=%d want 1", h.gotMsg.Load())
	}
}

func TestSession_SendAfterStop(t *testing.T) {
	// 会话关闭后 Send 返回 false。
	h := newEcho()
	srv := startServer(t, h, session.WithPingPeriod(0))
	c := dial(t, srv)

	// 关闭 client,等 server 检测到并 shutdown。
	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-h.closeCh:
	case <-time.After(time.Second):
		t.Fatal("OnClose not called")
	}
	if h.closed.Load() != 1 {
		t.Fatalf("closed=%d want 1", h.closed.Load())
	}
}

type blockingHandler struct {
	closeOnce sync.Once
	closeCh   chan struct{}
}

func (h *blockingHandler) OnOpen(*session.Session) error { return nil }
func (h *blockingHandler) OnMessage(s *session.Session, typ session.MessageType, data []byte) error {
	s.Send(typ, data)
	return nil
}
func (h *blockingHandler) OnClose(*session.Session, string) {
	h.closeOnce.Do(func() { close(h.closeCh) })
}

func TestSession_OnOpenError(t *testing.T) {
	h := &openErrHandler{closeCh: make(chan struct{})}
	srv := startServer(t, h, session.WithPingPeriod(0))
	c := dial(t, srv)
	// 连上后 OnOpen 报错,会话应立即关闭。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx) // 应读到 close 或 error
	if err == nil {
		t.Fatal("expected close/error")
	}
}

type openErrHandler struct {
	closeOnce sync.Once
	closeCh   chan struct{}
}

func (h *openErrHandler) OnOpen(*session.Session) error                                 { return errBoom }
func (h *openErrHandler) OnMessage(*session.Session, session.MessageType, []byte) error { return nil }
func (h *openErrHandler) OnClose(*session.Session, string) {
	h.closeOnce.Do(func() { close(h.closeCh) })
}

var errBoom = &boom{}

type boom struct{}

func (boom) Error() string { return "boom" }
