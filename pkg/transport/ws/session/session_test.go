package session_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rushteam/beauty/pkg/transport/ws"
	"github.com/rushteam/beauty/pkg/transport/ws/session"
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

// TestSession_Schedule 验证 Schedule 把回调投递到读循环 goroutine 串行执行。
func TestSession_Schedule(t *testing.T) {
	var scheduledVal atomic.Int32

	h := &scheduleHandler{
		closeCh: make(chan struct{}),
		onMsg: func(s *session.Session) {
			// 从 OnMessage(读线程)里 Schedule 一个回调
			ok := s.Schedule(func() {
				scheduledVal.Store(42)
			})
			if !ok {
				t.Error("Schedule should return true")
			}
		},
	}
	srv := startServer(t, h, session.WithPingPeriod(0))
	c := dial(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte("trigger")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// 读回声确认消息被处理
	_, _, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 给 drainSchedule 一点时间执行回调
	time.Sleep(50 * time.Millisecond)

	if scheduledVal.Load() != 42 {
		t.Fatalf("scheduled callback not executed, val=%d", scheduledVal.Load())
	}

	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-h.closeCh:
	case <-time.After(time.Second):
		t.Fatal("OnClose not called")
	}
}

// TestSession_ScheduleFromOtherGoroutine 从外部 goroutine 投递回调。
func TestSession_ScheduleFromOtherGoroutine(t *testing.T) {
	var scheduledVal atomic.Int32
	var sessionRef atomic.Pointer[session.Session]

	h := &scheduleHandler{
		closeCh: make(chan struct{}),
		onOpen: func(s *session.Session) {
			sessionRef.Store(s)
		},
	}
	srv := startServer(t, h, session.WithPingPeriod(0))
	c := dial(t, srv)

	// 等 OnOpen 完成
	time.Sleep(50 * time.Millisecond)

	s := sessionRef.Load()
	if s == nil {
		t.Fatal("session not captured")
	}

	// 从另一 goroutine Schedule
	ok := s.Schedule(func() {
		scheduledVal.Store(99)
	})
	if !ok {
		t.Fatal("Schedule should succeed")
	}

	// 发消息触发读循环处理 scheduleCh
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageText, []byte("ping"))
	_, _, _ = c.Read(ctx)

	time.Sleep(50 * time.Millisecond)
	if scheduledVal.Load() != 99 {
		t.Fatalf("scheduled from external goroutine not executed, val=%d", scheduledVal.Load())
	}

	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-h.closeCh:
	case <-time.After(time.Second):
	}
}

type scheduleHandler struct {
	closeOnce sync.Once
	closeCh   chan struct{}
	onMsg     func(s *session.Session)
	onOpen    func(s *session.Session)
}

func (h *scheduleHandler) OnOpen(s *session.Session) error {
	if h.onOpen != nil {
		h.onOpen(s)
	}
	return nil
}

func (h *scheduleHandler) OnMessage(s *session.Session, typ session.MessageType, data []byte) error {
	if h.onMsg != nil {
		h.onMsg(s)
	}
	s.Send(typ, data) // echo
	return nil
}

func (h *scheduleHandler) OnClose(s *session.Session, reason string) {
	h.closeOnce.Do(func() { close(h.closeCh) })
}

// ---- Attachment 测试 ----

func TestSession_Attachment(t *testing.T) {
	var sessionRef atomic.Pointer[session.Session]
	h := &scheduleHandler{
		closeCh: make(chan struct{}),
		onOpen: func(s *session.Session) {
			sessionRef.Store(s)
			s.Set("uid", "player-42")
			s.Set("level", 10)
		},
	}
	srv := startServer(t, h, session.WithPingPeriod(0))
	c := dial(t, srv)
	time.Sleep(50 * time.Millisecond)

	s := sessionRef.Load()
	if s == nil {
		t.Fatal("session not captured")
	}

	uid, ok := session.Get[string](s, "uid")
	if !ok || uid != "player-42" {
		t.Fatalf("Get uid=%q ok=%v", uid, ok)
	}
	level, ok := session.Get[int](s, "level")
	if !ok || level != 10 {
		t.Fatalf("Get level=%d ok=%v", level, ok)
	}

	// 类型不匹配
	_, ok = session.Get[float64](s, "uid")
	if ok {
		t.Fatal("wrong type should return false")
	}

	// 不存在
	_, ok = session.Get[string](s, "missing")
	if ok {
		t.Fatal("missing key should return false")
	}

	// Delete
	s.Delete("uid")
	_, ok = session.Get[string](s, "uid")
	if ok {
		t.Fatal("deleted key should return false")
	}

	// MustGet
	got := session.MustGet[int](s, "level")
	if got != 10 {
		t.Fatalf("MustGet=%d", got)
	}

	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-h.closeCh:
	case <-time.After(time.Second):
	}
}

// ---- Interceptor 测试 ----

func TestSession_Interceptor_Passthrough(t *testing.T) {
	var intercepted atomic.Int32
	interceptor := func(s *session.Session, typ session.MessageType, data []byte) (bool, error) {
		intercepted.Add(1)
		return false, nil // 不消费,继续传给 OnMessage
	}
	h := newEcho()
	srv := startServer(t, h, session.WithPingPeriod(0), session.WithInterceptor(interceptor))
	c := dial(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageText, []byte("hello"))
	_, data, _ := c.Read(ctx)
	if string(data) != "hello" {
		t.Fatalf("echo=%q", data)
	}
	if intercepted.Load() != 1 {
		t.Fatalf("interceptor calls=%d want 1", intercepted.Load())
	}

	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-h.closeCh:
	case <-time.After(time.Second):
	}
}

func TestSession_Interceptor_Consume(t *testing.T) {
	interceptor := func(s *session.Session, typ session.MessageType, data []byte) (bool, error) {
		if string(data) == "secret" {
			s.SendText([]byte("intercepted"))
			return true, nil // 消费掉,不传给 OnMessage
		}
		return false, nil
	}
	h := newEcho()
	srv := startServer(t, h, session.WithPingPeriod(0), session.WithInterceptor(interceptor))
	c := dial(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// secret 被拦截消费
	_ = c.Write(ctx, websocket.MessageText, []byte("secret"))
	_, data, _ := c.Read(ctx)
	if string(data) != "intercepted" {
		t.Fatalf("expected intercepted, got %q", data)
	}

	// 普通消息放行
	_ = c.Write(ctx, websocket.MessageText, []byte("normal"))
	_, data, _ = c.Read(ctx)
	if string(data) != "normal" {
		t.Fatalf("echo=%q", data)
	}

	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-h.closeCh:
	case <-time.After(time.Second):
	}
}

func TestSession_Interceptor_Error_ClosesSession(t *testing.T) {
	interceptor := func(s *session.Session, typ session.MessageType, data []byte) (bool, error) {
		return false, errors.New("auth failed")
	}
	h := newEcho()
	srv := startServer(t, h, session.WithPingPeriod(0), session.WithInterceptor(interceptor))
	c := dial(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageText, []byte("anything"))
	_, _, err := c.Read(ctx) // 应收到 close
	if err == nil {
		t.Fatal("interceptor error should close session")
	}

	select {
	case <-h.closeCh:
	case <-time.After(time.Second):
		t.Fatal("OnClose not called after interceptor error")
	}
}

// ---- Kick 测试 ----

func TestSession_Kick(t *testing.T) {
	var sessionRef atomic.Pointer[session.Session]
	wrapper := &kickTestHandler{
		inner: &scheduleHandler{
			closeCh: make(chan struct{}),
			onOpen: func(s *session.Session) {
				sessionRef.Store(s)
			},
		},
		reasonCh: make(chan string, 1),
		closeCh:  make(chan struct{}),
	}
	srv := startServer(t, wrapper, session.WithPingPeriod(0))
	c := dial(t, srv)
	time.Sleep(50 * time.Millisecond)

	s := sessionRef.Load()
	if s == nil {
		t.Fatal("session not captured")
	}

	// 踢人
	s.Kick(1, "duplicate login")

	// 客户端应收到 kick 消息
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read kick msg: %v", err)
	}
	if !strings.Contains(string(data), `"kick"`) {
		t.Fatalf("expected kick message, got %q", data)
	}
	if !strings.Contains(string(data), `"duplicate login"`) {
		t.Fatalf("kick reason not in message: %q", data)
	}

	// 等 OnClose
	select {
	case <-wrapper.closeCh:
	case <-time.After(time.Second):
		t.Fatal("OnClose not called")
	}
}

type kickTestHandler struct {
	inner    *scheduleHandler
	reasonCh chan string
	closeCh  chan struct{}
	once     sync.Once
}

func (h *kickTestHandler) OnOpen(s *session.Session) error { return h.inner.OnOpen(s) }
func (h *kickTestHandler) OnMessage(s *session.Session, typ session.MessageType, data []byte) error {
	return h.inner.OnMessage(s, typ, data)
}
func (h *kickTestHandler) OnClose(s *session.Session, reason string) {
	h.once.Do(func() { close(h.closeCh) })
	select {
	case h.reasonCh <- reason:
	default:
	}
}
