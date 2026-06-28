// router 示例:多语义消息路由 + 攒批。
//
// 演示 pkg/router:按 presence ID 定点投递、按 stream 群发、QueueDeferred 攒批。
// 配合 pkg/presence 查询频道成员,实现"发消息给某频道所有人"。
package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/presence"
	"github.com/rushteam/beauty/pkg/router"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

// sinkRegistry 把 sessionID 映射到一个本地投递函数(模拟 WebSocket Send)。
type sinkRegistry struct {
	mu    sync.Mutex
	sinks map[string]router.Sink
}

func (r *sinkRegistry) Lookup(sid string) router.Sink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sinks[sid]
}

func (r *sinkRegistry) set(sid string, sink router.Sink) {
	r.mu.Lock()
	r.sinks[sid] = sink
	r.mu.Unlock()
}

// forwarder 把跨节点消息打印到日志(模拟 RPC/消息总线转发到远端节点)。
type logForwarder struct{}

func (logForwarder) Forward(node string, ids []presence.ID, m router.Message) int {
	println("forward to node", node, ":", len(ids), "recipients, msg:", string(m.Data))
	return len(ids)
}

func main() {
	tr := presence.New(nil, 256)
	regs := &sinkRegistry{sinks: make(map[string]router.Sink)}
	rtr := router.New(regs, tr,
		router.WithLocalNode("node-a"),
		router.WithForwarder(logForwarder{}),
	)

	mux := http.NewServeMux()

	// /join?session=s1&user=alice&channel=room1  模拟连接加入。
	mux.HandleFunc("/join", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		sid := q.Get("session")
		stream := presence.Stream{Mode: 1, Subject: q.Get("channel")}
		tr.Track(sid, stream, presence.Meta{UserID: q.Get("user"), Username: q.Get("user")})
		// 注册 sink:投递到的消息打印到日志(模拟 ws.Send)。
		regs.set(sid, func(m router.Message) bool {
			println("deliver to", sid, ":", string(m.Data))
			return true
		})
		w.Write([]byte("joined"))
	})

	// /joinRemote?session=s9&user=bob&channel=room1&node=node-b  模拟远端在场成员。
	// 不注册本地 sink(会话在远端);Node 字段需由 presence 同步层写入 tracker,
	// 本 demo 仅演示 /send 路由直接构造带 Node 的 ID 触发跨节点转发。
	mux.HandleFunc("/joinRemote", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		stream := presence.Stream{Mode: 1, Subject: q.Get("channel")}
		tr.Track(q.Get("session"), stream, presence.Meta{UserID: q.Get("user")})
		w.Write([]byte("remote tracked (see /send for cross-node delivery)"))
	})

	// /say?channel=room1&msg=hello  群发给频道所有人(含跨节点)。
	mux.HandleFunc("/say", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		stream := presence.Stream{Mode: 1, Subject: q.Get("channel")}
		n := rtr.SendToStream(stream, router.Message{Data: []byte(q.Get("msg"))}, false)
		w.Write([]byte("delivered to " + itoa(n)))
	})

	// /send?session=s9&node=node-b&msg=hi  按 presence ID 定点投递(可跨节点)。
	mux.HandleFunc("/send", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		id := presence.ID{
			SessionID: q.Get("session"),
			Stream:    presence.Stream{Mode: 1, Subject: "direct"},
			Node:      q.Get("node"),
		}
		n := rtr.SendToPresenceIDs([]presence.ID{id}, router.Message{Data: []byte(q.Get("msg"))})
		w.Write([]byte("delivered to " + itoa(n)))
	})

	// /batch  演示攒批:投 3 条给 s1,一次 flush。
	mux.HandleFunc("/batch", func(w http.ResponseWriter, req *http.Request) {
		rtr.QueueDeferred([]string{"s1"}, router.Message{Data: []byte("msg1")})
		rtr.QueueDeferred([]string{"s1"}, router.Message{Data: []byte("msg2")})
		rtr.QueueDeferred([]string{"s1"}, router.Message{Data: []byte("msg3")})
		n := rtr.FlushDeferred()
		w.Write([]byte("flushed " + itoa(n)))
	})

	// 每 5 秒向 room1 攒批广播。
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			stream := presence.Stream{Mode: 1, Subject: "room1"}
			rtr.QueueDeferred(nil, router.Message{Data: []byte("tick")})
			_ = stream
		}
	}()

	app := beauty.New(beauty.WithWebServer(":8284", mux, webserver.WithServiceName("router-demo")))
	println("router demo on :8284")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
