// live-hls demo:端到端直播——RTMP 采集 → remux(FLV→MPEG-TS)→ HLS 分发,全程不转码。
//
//	推流端(OBS/ffmpeg) ──RTMP──▶ rtmp.Server ──remux.FLVToHLS──▶ hls.Stream ──HTTP──▶ 播放端
//
// 运行:
//
//	go run ./examples/live-hls
//
// 推流(另一个终端,需要 ffmpeg):
//
//	ffmpeg -re -stream_loop -1 -i input.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/demo
//
// 播放:
//
//	ffplay http://localhost:8090/live/index.m3u8
//	# 或浏览器/Safari 打开该 m3u8
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/hls"
	"github.com/rushteam/beauty/pkg/media/remux"
	"github.com/rushteam/beauty/pkg/media/rtmp"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	// HLS origin(6 分片滚动窗口,2s 目标时长;分片默认存内存,可换 hls.WithStore 落磁盘)。
	stream := hls.NewStream(hls.WithWindow(6), hls.WithTargetDuration(2*time.Second))

	// RTMP 采集:每一路 publish 用 remux 把 FLV 重封装成 TS 喂给 stream。
	rtmpSrv := rtmp.NewServer(":1935",
		func(streamKey string) rtmp.Handler {
			// 推流级鉴权:可在此按 streamKey(可含 ?token=…)校验,返回 nil 拒绝。
			log.Printf("publish started: %s", streamKey)
			return remux.NewFLVToHLS(stream)
		},
		rtmp.WithConnectAuth(func(ci *rtmp.ConnectInfo) error {
			// 连接级鉴权示例:按 app/tcUrl 校验签名等。这里放行。
			log.Printf("connect: app=%s tcUrl=%s", ci.App, ci.TCURL)
			return nil
		}),
		rtmp.WithServiceName("rtmp-ingest"),
	)

	// HLS 分发。
	mux := http.NewServeMux()
	mux.Handle("/live/", http.StripPrefix("/live", stream))

	app := beauty.New(
		beauty.WithService(rtmpSrv),
		beauty.WithWebServer(":8090", mux, webserver.WithServiceName("hls-origin")),
	)
	log.Println("RTMP ingest :1935  |  HLS play http://localhost:8090/live/index.m3u8")
	if err := app.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
}
