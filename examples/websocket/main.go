// WebSocket 示例：echo 端点 + JSON 广播端点。
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/stream"
	"github.com/rushteam/beauty/pkg/ws"
)

type notice struct {
	Seq int    `json:"seq"`
	Msg string `json:"msg"`
}

func main() {
	mux := http.NewServeMux()

	// 1) echo
	mux.Handle("/echo", ws.Handler(func(r *http.Request, c *ws.Conn) error {
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

	// 2) 广播：把通知 JSON 推给所有 /notice 连接
	hub := stream.New[notice](stream.WithBufferSize(64))
	mux.Handle("/notice", ws.BroadcastJSON(hub))
	go func() {
		for i := 0; ; i++ {
			time.Sleep(2 * time.Second)
			hub.Publish(notice{Seq: i, Msg: fmt.Sprintf("update %d", i)})
		}
	}()

	app := beauty.New(beauty.WithWebServer(":8080", mux, webserver.WithServiceName("ws-demo")))
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
