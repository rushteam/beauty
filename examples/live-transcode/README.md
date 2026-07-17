# live-transcode —— 拓扑 A:RTMP 采集 → ffmpeg 转码(ABR)→ HLS 分发

演示怎么把 **ffmpeg 转码**衔接进 beauty:框架收 RTMP、做鉴权/编排,把流管道喂给
ffmpeg 转成**多码率**(自适应码率 ABR),再用 [`pkg/hls.Master`](../../pkg/hls) 组主清单分发。
**转码交给 ffmpeg,框架不碰编解码。**

```
OBS/ffmpeg ──RTMP──▶ pkg/media/rtmp ──重建 FLV──▶ ffmpeg(720p+360p 两档)──▶ HLS 目录
                                                                              │
                        pkg/hls.Master(FileServer 变体)◀── master + 各码率 ──┘
```

衔接的关键点:`rtmp.Handler` 把收到的音视频 tag 重新拼成合法 **FLV 流**写进
`ffmpeg -f flv -i pipe:0` 的 stdin;ffmpeg 一个进程输出两路 HLS 到 `./hls-out/{720p,360p}`;
`Master` 的两个变体各用一个(补了 `.m3u8/.ts` content-type 的)`http.FileServer` 分发。

## 运行(需要 PATH 里有 ffmpeg)

```bash
go run ./examples/live-transcode
```

推流:

```bash
ffmpeg -re -stream_loop -1 -i input.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/demo
```

播放(播放器会按带宽在 720p/360p 间自适应切换):

```bash
ffplay http://localhost:8090/live/master.m3u8
```

## 说明

- 转码档位/码率/编码参数都在 ffmpeg 命令里调(`-b:v`/`-s`/`-preset`/`-c:v` 等);要 H.265/AV1 换 `-c:v` 即可。
- 本示例单路推流、固定输出目录;多主播要按 `streamKey` 分目录 + 各自 ffmpeg 进程 + 生命周期管理。
- ffmpeg 进程的崩溃重启、背压、资源上限等生产细节未做,示例聚焦"衔接点"本身。
- 对比:不转码的低开销链路见 [`examples/live-hls`](../live-hls)(rtmp→remux copy→hls)。
