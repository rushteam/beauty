package ws

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/stream"
)

// BroadcastJSON 返回一个只推送的 WebSocket handler：每个连接订阅 b，
// 把广播来的每个值以 JSON 文本帧发给客户端。连接断开时自动退订。
//
// 它在后台 CloseRead 以处理客户端的 ping/pong 与关闭帧（写多读少场景的推荐做法）。
//
//	bc := stream.New[Notice](stream.WithBufferSize(64))
//	mux.Handle("/ws/notice", ws.BroadcastJSON(bc))
//	// 业务侧：bc.Publish(Notice{...})
func BroadcastJSON[T any](b *stream.Broadcaster[T], opts ...Option) http.HandlerFunc {
	return Handler(func(r *http.Request, c *Conn) error {
		// 后台读：处理控制帧，连接关闭时取消 readCtx
		readCtx := c.Raw().CloseRead(r.Context())
		ch, cancel := b.Subscribe(readCtx)
		defer cancel()
		for {
			select {
			case <-readCtx.Done():
				return nil
			case v, ok := <-ch:
				if !ok {
					return nil
				}
				if err := c.WriteJSON(readCtx, v); err != nil {
					return err
				}
			}
		}
	}, opts...)
}
