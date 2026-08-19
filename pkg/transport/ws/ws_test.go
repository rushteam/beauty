package ws

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

// 启动一个 httptest 服务并返回 ws:// 地址。
func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dial(t *testing.T, url string, opts *websocket.DialOptions) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

func TestHandler_Echo(t *testing.T) {
	url := serve(t, Handler(func(r *http.Request, c *Conn) error {
		ctx := r.Context()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return err
			}
			if err := c.Write(ctx, typ, data); err != nil {
				return err
			}
		}
	}))

	c := dial(t, url, nil)
	defer c.CloseNow()

	ctx := context.Background()
	if err := c.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText || string(data) != "ping" {
		t.Fatalf("echo mismatch: typ=%v data=%q", typ, data)
	}
	c.Close(websocket.StatusNormalClosure, "")
}

func TestHandler_JSONRoundTrip(t *testing.T) {
	type msg struct {
		N int    `json:"n"`
		S string `json:"s"`
	}
	url := serve(t, Handler(func(r *http.Request, c *Conn) error {
		var in msg
		if err := c.ReadJSON(r.Context(), &in); err != nil {
			return err
		}
		in.N++
		return c.WriteJSON(r.Context(), in)
	}))

	c := dial(t, url, nil)
	defer c.CloseNow()
	ctx := context.Background()

	if err := wsjson.Write(ctx, c, msg{N: 1, S: "hi"}); err != nil {
		t.Fatalf("write json: %v", err)
	}
	var out msg
	if err := wsjson.Read(ctx, c, &out); err != nil {
		t.Fatalf("read json: %v", err)
	}
	if out.N != 2 || out.S != "hi" {
		t.Fatalf("json round-trip mismatch: %+v", out)
	}
}

// handler 应能读取请求内容（query / header）。
func TestHandler_ReadsRequest(t *testing.T) {
	url := serve(t, Handler(func(r *http.Request, c *Conn) error {
		topic := r.URL.Query().Get("topic")
		token := r.Header.Get("X-Token")
		return c.WriteText(r.Context(), topic+"|"+token)
	}))

	c := dial(t, url+"?topic=orders", &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Token": []string{"abc"}},
	})
	defer c.CloseNow()

	_, data, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "orders|abc" {
		t.Fatalf("want %q, got %q", "orders|abc", string(data))
	}
}

// 子协议协商。
func TestHandler_Subprotocol(t *testing.T) {
	url := serve(t, Handler(func(r *http.Request, c *Conn) error {
		return c.WriteText(r.Context(), c.Subprotocol())
	}, WithSubprotocols("chat.v1")))

	c := dial(t, url, &websocket.DialOptions{Subprotocols: []string{"chat.v1"}})
	defer c.CloseNow()

	_, data, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "chat.v1" {
		t.Fatalf("want negotiated subprotocol chat.v1, got %q", string(data))
	}
}

// 开启心跳后，健康连接应保持正常 echo（ping 不影响数据帧）。
func TestHandler_PingKeepsAlive(t *testing.T) {
	url := serve(t, Handler(func(r *http.Request, c *Conn) error {
		ctx := r.Context()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return err
			}
			if err := c.Write(ctx, typ, data); err != nil {
				return err
			}
		}
	}, WithPingInterval(30*time.Millisecond)))

	c := dial(t, url, nil)
	defer c.CloseNow()
	ctx := context.Background()

	// 持续读使 pong 得到处理；跨多个 ping 周期仍能正常往返
	for i := range 3 {
		if err := c.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if string(data) != "hi" {
			t.Fatalf("echo %d mismatch: %q", i, string(data))
		}
		time.Sleep(40 * time.Millisecond) // 跨过一个 ping 周期
	}
}

// fn 返回 nil 时应正常关闭（客户端收到 StatusNormalClosure）。
func TestHandler_NormalClose(t *testing.T) {
	url := serve(t, Handler(func(r *http.Request, c *Conn) error {
		return nil // 立即正常关闭
	}))

	c := dial(t, url, nil)
	defer c.CloseNow()

	_, _, err := c.Read(context.Background())
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("want normal closure, got err=%v status=%v", err, websocket.CloseStatus(err))
	}
}
