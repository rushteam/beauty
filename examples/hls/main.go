// hls demo:用 pkg/hls 起一个直播 origin,挂在 webserver 上,后台每 2s 喂一个
// 合成分片(内容是占位字节,非真实 TS——本示例只演示 origin 的播放列表/分片分发,
// 不涉及编解码)。
//
// 运行:
//
//	go run ./examples/hls
//	# 另开一个终端:
//	curl -s localhost:8090/live/index.m3u8      # 看滚动播放列表
//	curl -s localhost:8090/live/seg0.ts         # 拉某个分片(占位数据)
//
// 真实场景里,分片由 ffmpeg 或 pkg/media/rtmp 采集后的 remux 产出,再 Append 进来。
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/hls"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	stream := hls.NewStream(hls.WithWindow(5), hls.WithTargetDuration(2*time.Second))

	mux := http.NewServeMux()
	mux.Handle("/live/", http.StripPrefix("/live", stream))

	// 后台每 2s 产出一个合成分片(真实场景由 ffmpeg / rtmp remux 提供)。
	go func() {
		for i := 0; ; i++ {
			data := fmt.Appendf(nil, "fake-ts-segment-%d", i)
			seq, _ := stream.Append(data, 2*time.Second)
			log.Printf("appended segment seq=%d (%d bytes)", seq, len(data))
			time.Sleep(2 * time.Second)
		}
	}()

	app := beauty.New(beauty.WithWebServer(":8090", mux, webserver.WithServiceName("hls-origin")))
	if err := app.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
}
