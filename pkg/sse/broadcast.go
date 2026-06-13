package sse

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/stream"
)

// Broadcast 返回一个 SSE handler：每个连接订阅 b，并把广播来的事件逐条推送给客户端。
// 连接断开（r.Context() 取消）时自动退订。配合 stream.Broadcaster 可实现
// “一个事件源推给 N 个 SSE 连接”。
//
//	bc := stream.New[sse.Event](stream.WithBufferSize(64))
//	mux.Handle("/events", sse.Broadcast(bc))
//	// 业务侧产生事件：bc.Publish(sse.Event{Event: "msg", Data: "..."})
func Broadcast(b *stream.Broadcaster[Event], opts ...Option) http.HandlerFunc {
	return Handler(func(r *http.Request, sink Sink) error {
		ch, cancel := b.Subscribe(r.Context())
		defer cancel()
		for ev := range ch {
			if err := sink.Send(ev); err != nil {
				return err
			}
		}
		return nil
	}, opts...)
}
