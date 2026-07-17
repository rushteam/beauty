// live-multi demo:多路直播——一个 RTMP 端口收多路流,按 streamKey 各自成流、各自分发,
// 用 pkg/media.Hub 做多路管理 + OTel 指标。走 copy 路径(remux,不转码)。
//
//	OBS/ffmpeg(推 /live/roomA)─┐
//	OBS/ffmpeg(推 /live/roomB)─┼─▶ rtmp.Server ─▶ Hub.Acquire(key) ─▶ remux→各自 hls.Stream
//	                            ┘                        │
//	                     播放 /live/roomA/index.m3u8 ◀── Hub 按 key 路由分发
//
// 运行:
//
//	go run ./examples/live-multi
//
// 推两路(不同 streamKey → 两路互不干扰):
//
//	ffmpeg -re -stream_loop -1 -i a.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/roomA
//	ffmpeg -re -stream_loop -1 -i b.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/roomB
//
// 播放:
//
//	ffplay http://localhost:8090/live/roomA/index.m3u8
//	curl   http://localhost:8090/streams          # 当前在线流列表
//
// 指标:所有 media.* 指标走 OTel 全局 MeterProvider。用 beauty 的 telemetry 组件配好
// 导出器(OTLP/Prometheus)后即可采集 media.streams.active / media.ingest.bytes 等;
// 未配置时为 no-op。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/hls"
	"github.com/rushteam/beauty/pkg/media"
	"github.com/rushteam/beauty/pkg/media/remux"
	"github.com/rushteam/beauty/pkg/media/rtmp"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	// 每路流用 6 分片、2s 目标时长的 hls.Stream(policy 在工厂里定)。
	hub := media.NewHub(media.WithStreamFactory(func(key string) *hls.Stream {
		return hls.NewStream(hls.WithWindow(6), hls.WithTargetDuration(2*time.Second))
	}))

	// RTMP 采集:每路 publish 用 streamKey 抢占一路 Session;重复推流被拒。
	rtmpSrv := rtmp.NewServer(":1935", func(streamKey string) rtmp.Handler {
		sess, ok := hub.Acquire(streamKey)
		if !ok {
			return nil // 该 key 已在推流,拒绝(防抢流)
		}
		return &ingest{
			Handler: remux.NewFLVToHLS(sess.Stream), // copy 路径:FLV→TS 写入该路 Stream
			key:     streamKey,
			metrics: hub.Metrics(),
			release: func() { hub.Release(streamKey) },
		}
	}, rtmp.WithServiceName("rtmp-ingest"))

	mux := http.NewServeMux()
	mux.Handle("/live/", http.StripPrefix("/live", hub)) // 按 key 路由分发
	mux.HandleFunc("/streams", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]int{"active": hub.Count()})
	})

	app := beauty.New(
		beauty.WithService(rtmpSrv),
		beauty.WithWebServer(":8090", mux, webserver.WithServiceName("hls-origin")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

// ingest 包一层 remux handler:记录采集入流量指标,并在推流结束时 Release 该路 Session。
type ingest struct {
	rtmp.Handler
	key     string
	metrics *media.Metrics
	release func()
}

func (i *ingest) OnAudio(ts uint32, d []byte) error {
	i.metrics.IngestBytes(context.Background(), i.key, int64(len(d)))
	return i.Handler.OnAudio(ts, d)
}

func (i *ingest) OnVideo(ts uint32, d []byte) error {
	i.metrics.IngestBytes(context.Background(), i.key, int64(len(d)))
	return i.Handler.OnVideo(ts, d)
}

func (i *ingest) OnClose() {
	i.Handler.OnClose()
	i.release()
}
