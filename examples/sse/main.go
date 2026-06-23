// SSE 示例：一个定时推送端点 + 一个广播端点。
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/sse"
	"github.com/rushteam/beauty/pkg/stream"
)

func main() {
	mux := http.NewServeMux()

	// 1) 每秒给当前连接推一条时间
	mux.Handle("/time", sse.Handler(func(r *http.Request, sink sse.Sink) error {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return r.Context().Err()
			case t := <-ticker.C:
				if err := sink.Send(sse.Event{Event: "tick", Data: t.Format(time.RFC3339)}); err != nil {
					return err
				}
			}
		}
	}))

	// 2) 广播：一个事件源 fan-out 给所有 /news 连接
	news := stream.New[sse.Event](stream.WithBufferSize(64))
	mux.Handle("/news", sse.Broadcast(news))
	go func() {
		for i := 0; ; i++ {
			time.Sleep(2 * time.Second)
			news.Publish(sse.Event{Event: "news", Data: fmt.Sprintf("headline #%d", i)})
		}
	}()

	app := beauty.New(beauty.WithWebServer(":8080", mux, webserver.WithServiceName("sse-demo")))
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
