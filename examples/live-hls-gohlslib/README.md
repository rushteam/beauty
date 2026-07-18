# live-hls-gohlslib —— RTMP 推流 → gohlslib → LL-HLS

用 [`pkg/media/hlsmux`](../../pkg/media/hlsmux)(基于 mediamtx 同款 `bluenviron/gohlslib`)
把 RTMP 推流转成 **LL-HLS**。相比自研的 [`pkg/media/remux`](../../pkg/media/remux)(仅 FLV→TS +
手搓播放列表),这条路径由 gohlslib 负责分片、播放列表(含 LL-HLS `EXT-X-PART`/`PRELOAD-HINT`
与阻塞式刷新)、fMP4 init 段等全部 HLS 细节。

```
OBS/ffmpeg(推 /live/stream) ─▶ rtmp.Server ─▶ hlsmux.Bridge(FLV→gohlslib)
                                                  │
                     播放 http://localhost:8080/  ◀── LL-HLS(/hls/index.m3u8)
```

## 运行

```bash
go run ./examples/live-hls-gohlslib
# 推流(H.264 + AAC):
ffmpeg -re -stream_loop -1 -i input.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/stream
# 播放:浏览器打开 http://localhost:8080/(hls.js;Safari 原生支持)
```

## 两条 HLS 路径怎么选

| | `pkg/hls` + `pkg/media/remux` | `pkg/media/hlsmux`(本示例) |
|---|---|---|
| HLS 实现 | 自研(播放列表/TS 切片手写) | gohlslib(mediamtx 同款库) |
| LL-HLS / fMP4 | pkg/hls 支持 LL-HLS;remux 只产 TS | 原生支持 TS / fMP4 / LL-HLS |
| 第三方依赖 | 仅 go-astits | gohlslib + mediacommon |
| 适合 | 最薄、够用、想少依赖 | 要产线级 HLS、经真实播放器打磨 |

两条都在,按需取用;本示例演示 gohlslib 这条。

## 边界(机制 vs 策略)

- **单路 demo**:一个 `Bridge` = 一路流。多路请每个 `streamKey` 建一个 `Bridge`,用
  [`pkg/media.Hub`](../../pkg/media) 管理路由(参考 [`examples/live-multi`](../live-multi))。
- **只桥接 H.264 + AAC**(OBS/ffmpeg 默认);其它编码丢弃。
- 分片时长、LL-HLS 参数、落盘目录经 `hlsmux.With*` 透出;转码、鉴权、多码率仍在框架外。
- 真机播放器互操作请用真实流校验(单测用 gohlslib 自身校验产出结构)。
