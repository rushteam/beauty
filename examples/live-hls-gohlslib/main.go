// live-hls-gohlslib demo:RTMP 推流 → gohlslib → LL-HLS 播放。展示 pkg/media/hlsmux
// (基于 bluenviron/gohlslib)这条 HLS 路径:相比自研的 pkg/media/remux,
// 由 gohlslib 负责分片/播放列表/LL-HLS/fMP4 等全部 HLS 细节。
//
//	OBS/ffmpeg(推 /live/stream)─▶ rtmp.Server ─▶ hlsmux.Bridge(FLV→gohlslib)
//	                                                  │
//	                     播放 http://localhost:8080/  ◀── LL-HLS(/hls/index.m3u8)
//
// 运行:
//
//	go run ./examples/live-hls-gohlslib
//	# 推流(H.264 + AAC,OBS/ffmpeg 默认组合):
//	ffmpeg -re -stream_loop -1 -i input.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/stream
//	# 播放:浏览器打开 http://localhost:8080/(hls.js;Safari 原生支持 HLS)
//
// 说明:本 demo 是单路流(一个 Bridge = 一路)。多路请每个 streamKey 建一个 Bridge,
// 用 pkg/media.Hub 管理(参考 examples/live-multi 的多路模式)。
package main

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/media/hlsmux"
	"github.com/rushteam/beauty/pkg/media/rtmp"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	// LL-HLS(默认 variant)。Bridge 同时是 rtmp.Handler(收流)与 http.Handler(播放)。
	bridge := hlsmux.NewBridge(hlsmux.WithVariant(hlsmux.VariantLowLatency))

	rtmpSrv := rtmp.NewServer(":1935", func(streamKey string) rtmp.Handler {
		return bridge // 单路 demo:所有推流都进这一个 Bridge
	}, rtmp.WithServiceName("rtmp-ingest"))

	mux := http.NewServeMux()
	mux.Handle("/hls/", http.StripPrefix("/hls", bridge)) // 播放列表/分片
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(playerHTML))
	})

	app := beauty.New(
		beauty.WithService(rtmpSrv),
		beauty.WithWebServer(":8080", mux, webserver.WithServiceName("hls-origin")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

// 播放页(policy):Safari 原生支持 HLS,其它浏览器用 hls.js。
const playerHTML = `<!doctype html><meta charset=utf-8><title>LL-HLS</title>
<h3>LL-HLS 播放(gohlslib)</h3>
<video id=v controls autoplay muted playsinline style="width:720px;background:#000"></video>
<p>推流地址:<code>rtmp://localhost:1935/live/stream</code></p>
<script src="https://cdn.jsdelivr.net/npm/hls.js@1"></script>
<script>
const v=document.getElementById('v'), src='/hls/index.m3u8';
if(v.canPlayType('application/vnd.apple.mpegurl')){ v.src=src; }
else if(window.Hls&&Hls.isSupported()){ const h=new Hls({lowLatencyMode:true}); h.loadSource(src); h.attachMedia(v); }
else { document.body.append('浏览器不支持 HLS'); }
</script>`
