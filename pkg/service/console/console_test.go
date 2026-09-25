package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestRegisterAndGetCommand(t *testing.T) {
	s := New()
	s.Register(&Command{Name: "foo", Handler: func(context.Context, *Ctx) (string, error) { return "bar", nil }})
	if s.getCommand("foo") == nil {
		t.Fatal("expected foo registered")
	}
	// 非法命令被忽略。
	s.Register(nil)
	s.Register(&Command{Name: "", Handler: func(context.Context, *Ctx) (string, error) { return "", nil }})
	s.Register(&Command{Name: "noHandler"})
	if s.getCommand("noHandler") != nil {
		t.Fatal("command without handler should be ignored")
	}
}

func TestBuiltinsRegistered(t *testing.T) {
	s := New()
	for _, name := range []string{"help", "ping", "env", "mem", "gc", "goroutine", "deadlock", "topics", "sub", "unsub"} {
		if s.getCommand(name) == nil {
			t.Errorf("builtin %q not registered", name)
		}
	}
	if s.getTopic("top") == nil {
		t.Error("default topic 'top' not registered")
	}
}

func TestAuthGating(t *testing.T) {
	// 无账号 => 开发模式,全部授权。
	dev := New()
	if dev.authRequired() {
		t.Error("dev mode should not require auth")
	}
	out, err := dev.runCommand(context.Background(), "env", false, "", &client{})
	if err != nil {
		t.Fatalf("dev mode env should run: %v", err)
	}
	_ = out

	// 配置账号 => 受保护命令未登录被拒,public 命令仍可执行。
	s := New(WithUser("admin", "secret"))
	if !s.checkLogin("admin", "secret") || s.checkLogin("admin", "wrong") {
		t.Error("checkLogin behaves incorrectly")
	}
	if _, err := s.runCommand(context.Background(), "env", false, "", &client{}); err == nil {
		t.Error("protected command should require auth")
	}
	if _, err := s.runCommand(context.Background(), "ping", false, "", &client{}); err != nil {
		t.Errorf("public command should run unauthenticated: %v", err)
	}
}

func TestHintFiltering(t *testing.T) {
	s := New(WithUser("a", "b"))
	// 未授权:只返回 public 命令。
	got := s.hint("", false)
	for _, h := range got {
		cmd := s.getCommand(h.Name)
		if !cmd.isPublic() {
			t.Errorf("hint returned non-public command %q for unauthorized user", h.Name)
		}
	}
	// 前缀过滤。
	got = s.hint("hel", true)
	if len(got) != 1 || got[0].Name != "help" {
		t.Errorf("hint(hel) = %+v, want [help]", got)
	}
}

func TestRunCommandUnknownAndPanic(t *testing.T) {
	s := New()
	if _, err := s.runCommand(context.Background(), "nope", true, "", &client{}); err == nil {
		t.Error("unknown command should error")
	}
	s.Register(&Command{Name: "boom", Handler: func(context.Context, *Ctx) (string, error) { panic("kaboom") }})
	_, err := s.runCommand(context.Background(), "boom", true, "", &client{})
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Errorf("panic should be recovered as error, got %v", err)
	}
}

// TestWebSocketRoundTrip 端到端验证:页面可访问、WS 可执行命令并推送 topic。
func TestWebSocketRoundTrip(t *testing.T) {
	s := New()
	// 缩短 top 主题间隔以便快速验证推送。
	s.getTopic("top").Interval = 50 * time.Millisecond

	mux := http.NewServeMux()
	s.Handler(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 启动 top 主题推送循环。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.getTopic("top").run(ctx)

	// 页面可访问。
	resp, err := http.Get(srv.URL + "/console")
	if err != nil {
		t.Fatalf("GET page: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page status = %d", resp.StatusCode)
	}

	// 连接 WS。
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/console/ws"
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	c, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer c.CloseNow()

	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()

	// 第一条应为 welcome。
	var welcome response
	if err := wsjson.Read(readCtx, c, &welcome); err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if welcome.Type != "welcome" {
		t.Fatalf("first msg type = %q, want welcome", welcome.Type)
	}

	// 执行 ping。
	if err := wsjson.Write(readCtx, c, request{ID: 1, Action: "command", Command: "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	var pong response
	if err := wsjson.Read(readCtx, c, &pong); err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if !pong.OK || pong.Data != "pong" {
		t.Fatalf("ping result = %+v, want pong", pong)
	}

	// 订阅 top 并等待一次推送。
	if err := wsjson.Write(readCtx, c, request{ID: 2, Action: "command", Command: "sub top"}); err != nil {
		t.Fatalf("write sub: %v", err)
	}
	// 读到 sub 的确认或 push,直到出现一次 push。
	gotPush := false
	for i := 0; i < 10 && !gotPush; i++ {
		var msg response
		if err := wsjson.Read(readCtx, c, &msg); err != nil {
			t.Fatalf("read after sub: %v", err)
		}
		if msg.Type == "push" && msg.Push == "top" {
			gotPush = true
		}
	}
	if !gotPush {
		t.Fatal("did not receive a top push within expected messages")
	}
}
