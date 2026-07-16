# hls —— 用 pkg/hls 起一个直播 origin

演示 [`pkg/hls`](../../pkg/hls):把一路 HLS 流挂在 `webserver` 上分发,后台每 2s 喂一个
**合成分片**(占位字节,非真实 TS——本示例只演示 origin 的「播放列表 + 分片分发」,不碰编解码)。

## 运行

```bash
go run ./examples/hls
```

另开终端:

```bash
curl -s localhost:8090/live/index.m3u8   # 滚动 media playlist(窗口 5)
curl -s localhost:8090/live/seg0.ts      # 拉某个分片(占位数据)
```

播放列表长这样(直播中,EVENT 类型,无 ENDLIST):

```
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:3
#EXT-X-PLAYLIST-TYPE:EVENT
#EXTINF:2.000,
seg3.ts
#EXTINF:2.000,
seg4.ts
...
```

## 真实管道怎么接

本示例的分片是假的。真实场景:

```
推流端(OBS/ffmpeg) ──RTMP──▶ pkg/media/rtmp 采集 ──remux──▶ pkg/hls.Append ──HTTP──▶ 播放端
                              (或直接 ffmpeg 切片喂 Append)
```

- 采集:见 [`pkg/media/rtmp`](../../pkg/media/rtmp)(接 OBS/ffmpeg 推流,拿到 FLV 音视频)。
- remux(FLV→TS/fMP4 分片,不转码):目前需自行接入(astits / gohlslib 等),是把两端
  串成端到端直播的最后一环。
- 分发:`pkg/hls`(本示例)。
