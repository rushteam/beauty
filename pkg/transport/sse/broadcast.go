package sse

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/messaging/stream"
)

// Broadcast 返回一个 SSE handler：每个连接订阅 b，并把广播来的事件逐条推送给客户端。
// 连接断开（r.Context() 取消）时自动退订。配合 stream.Broadcaster 可实现
// “一个事件源推给 N 个 SSE 连接”。
//
//	bc := stream.New[sse.Event](stream.WithBufferSize(64))
//	mux.Handle("/events", sse.Broadcast(bc))
//	// 业务侧产生事件：bc.Publish(sse.Event{Event: "msg", Data: "..."})
func Broadcast(b *stream.Broadcaster[Event], opts ...Option) http.HandlerFunc {
	return Stream(func(r *http.Request) (<-chan Event, func(), error) {
		ch, cancel := b.Subscribe(r.Context())
		return ch, cancel, nil
	}, opts...)
}

// Stream 返回一个 SSE handler：每个连接调用 subscribe 取得事件 channel 并逐条推送，
// channel 关闭或连接断开时结束并调用其取消函数。用于按 key 分组的事件源
// （stream.Hub 单机多房间、stream.Bridge 跨节点多房间）：
//
//	rooms := stream.NewBridge(ps, ps, stream.TopicPrefix[string]("sse.room."), stream.JSON[sse.Event]())
//	mux.Handle("GET /rooms/{room}/events", sse.Stream(func(r *http.Request) (<-chan sse.Event, func(), error) {
//	    return rooms.Subscribe(r.Context(), r.PathValue("room"))
//	}))
//
// subscribe 返回错误时以注释形式告知客户端后结束（此时响应头已发出）；
// 需要按错误返回 4xx 的鉴权 / 参数校验应放在外层中间件。
func Stream(subscribe func(r *http.Request) (<-chan Event, func(), error), opts ...Option) http.HandlerFunc {
	return Handler(func(r *http.Request, sink Sink) error {
		ch, cancel, err := subscribe(r)
		if err != nil {
			return err
		}
		defer cancel()
		for {
			select {
			case <-r.Context().Done():
				return nil
			case ev, ok := <-ch:
				if !ok {
					return nil
				}
				if err := sink.Send(ev); err != nil {
					return err
				}
			}
		}
	}, opts...)
}
