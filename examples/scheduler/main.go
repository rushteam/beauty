// scheduler 示例:工作池 + 暂停/恢复。
//
// 演示 pkg/scheduler:Submit 投递任务、N worker 并发消费、Pause/Resume 运行时控制。
// HTTP 端点投递任务并查询处理数;POST /pause 与 /resume 控制消费。
package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/scheduler"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	var processed atomic.Int64
	s := scheduler.New(
		scheduler.WithWorkers(3),
		scheduler.WithQueueSize(100),
		scheduler.WithErrorHandler(func(name string, err error, stack []byte) {
			println("task error:", name, err)
		}),
	)
	s.Start(context.Background())

	mux := http.NewServeMux()

	// /enqueue  投递 10 个任务。
	mux.HandleFunc("/enqueue", func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 10; i++ {
			s.Submit(&scheduler.Task{
				Name: "work",
				Fn: func(ctx context.Context) error {
					time.Sleep(100 * time.Millisecond) // 模拟耗时
					processed.Add(1)
					return nil
				},
			})
		}
		w.Write([]byte("enqueued 10 tasks"))
	})

	// /pause  暂停消费。
	mux.HandleFunc("/pause", func(w http.ResponseWriter, r *http.Request) {
		s.Pause()
		w.Write([]byte("paused"))
	})

	// /resume  恢复消费。
	mux.HandleFunc("/resume", func(w http.ResponseWriter, r *http.Request) {
		s.Resume()
		w.Write([]byte("resumed"))
	})

	// /stats  查询处理进度。
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("processed=" + itoa(int(processed.Load())) + " pending=" + itoa(s.Pending())))
	})

	app := beauty.New(beauty.WithWebServer(":8286", mux, webserver.WithServiceName("scheduler-demo")))
	println("scheduler demo on :8286")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
