// session 示例:有状态 WebSocket 会话,echo + 广播。
//
// 演示 pkg/ws/session 的双 goroutine 读写模型、ping/pong 心跳、关闭握手。
// 连入 /ws 即成为会话,发消息被 echo 回来;同时每 2 秒收到一条服务端心跳广播。
package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/ws"
	"github.com/rushteam/beauty/pkg/ws/session"
)

type room struct {
	mu      sync.Mutex
	clients map[*session.Session]struct{}
}

func newRoom() *room {
	return &room{clients: make(map[*session.Session]struct{})}
}

func (r *room) add(s *session.Session) {
	r.mu.Lock()
	r.clients[s] = struct{}{}
	r.mu.Unlock()
}

func (r *room) remove(s *session.Session) {
	r.mu.Lock()
	delete(r.clients, s)
	r.mu.Unlock()
}

func (r *room) broadcast(msg []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for s := range r.clients {
		s.SendText(msg) // 非阻塞,慢客户端队列满会自动关闭
	}
}

type handler struct {
	room *room
}

func (h *handler) OnOpen(s *session.Session) error {
	h.room.add(s)
	s.SendText([]byte("welcome"))
	return nil
}

func (h *handler) OnMessage(s *session.Session, typ session.MessageType, data []byte) error {
	// echo + 广播给房间所有人。
	h.room.broadcast(append([]byte("broadcast: "), data...))
	return nil
}

func (h *handler) OnClose(s *session.Session, reason string) {
	h.room.remove(s)
}

func main() {
	room := newRoom()
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(session.Accept(&handler{room: room},
		session.WithPingPeriod(30*time.Second),
		session.WithPingTimeout(5*time.Second),
	), ws.WithInsecureSkipVerify()))

	// 每 2 秒广播一次心跳。
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			room.broadcast([]byte("heartbeat"))
		}
	}()

	app := beauty.New(beauty.WithWebServer(":8282", mux, webserver.WithServiceName("session-demo")))
	println("session demo on :8282  (ws://localhost:8282/ws)")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
