// live-multi demo:多路直播——一个 RTMP 端口收多路流,按 streamKey 各自成流、各自分发,
// 用 pkg/media.Hub 做多路管理 + OTel 指标,每路 HLS 由 pkg/media/hlsmux(gohlslib)产出
// LL-HLS。
//
//	OBS/ffmpeg(推 /live/roomA)─┐
//	OBS/ffmpeg(推 /live/roomB)─┼─▶ rtmp.Server ─▶ Hub.Acquire(key) ─▶ hlsmux.Bridge(→LL-HLS)
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
//	curl   http://localhost:8090/streams          # 当前在线流数
//
// 指标:所有 media.* 指标走 OTel 全局 MeterProvider。配好导出器(OTLP/Prometheus)后即可
// 采集 media.streams.active / media.ingest.bytes 等;未配置时为 no-op。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/media"
	"github.com/rushteam/beauty/pkg/media/hlsmux"
	"github.com/rushteam/beauty/pkg/media/rtmp"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	// 每路流一个 gohlslib Bridge(LL-HLS,2s 分片)。Hub 按 key 管理这些 Bridge。
	hub := media.NewHub(func(key string) *hlsmux.Bridge {
		return hlsmux.NewBridge(
			hlsmux.WithVariant(hlsmux.VariantLowLatency),
			hlsmux.WithSegmentMinDuration(2*time.Second),
		)
	})

	// RTMP 采集:每路 publish 用 streamKey 抢占一路 Session;重复推流被拒。
	rtmpSrv := rtmp.NewServer(":1935", func(streamKey string) rtmp.Handler {
		sess, ok := hub.Acquire(streamKey)
		if !ok {
			return nil // 该 key 已在推流,拒绝(防抢流)
		}
		return &ingest{
			Bridge:  sess.Stream, // Bridge 同时是 rtmp.Handler(收流)与 http.Handler(播放)
			key:     streamKey,
			metrics: hub.Metrics(),
			release: func() { hub.Release(streamKey) },
		}
	}, rtmp.WithServiceName("rtmp-ingest"))

	mux := http.NewServeMux()
	mux.Handle("/live/", http.StripPrefix("/live", hub)) // 按 key 路由分发
	mux.HandleFunc("/streams", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int{"active": hub.Count()})
	})

	app := beauty.New(
		beauty.WithService(rtmpSrv),
		beauty.WithWebServer(":8090", mux, webserver.WithServiceName("hls-origin")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

// ingest 包一层 Bridge:记录采集入流量指标,并在推流结束时 Release 该路 Session。
// 嵌入 *hlsmux.Bridge → OnMetaData 等方法自动提升;这里只覆盖要加料的几个。
type ingest struct {
	*hlsmux.Bridge
	key     string
	metrics *media.Metrics
	release func()
}

func (i *ingest) OnAudio(ts uint32, d []byte) error {
	i.metrics.IngestBytes(context.Background(), i.key, int64(len(d)))
	return i.Bridge.OnAudio(ts, d)
}

func (i *ingest) OnVideo(ts uint32, d []byte) error {
	i.metrics.IngestBytes(context.Background(), i.key, int64(len(d)))
	return i.Bridge.OnVideo(ts, d)
}

func (i *ingest) OnClose() {
	i.Bridge.OnClose()
	i.release()
}
