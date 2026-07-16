# live-hls —— 端到端直播:RTMP 采集 → remux → HLS 分发

把本 session 的三块拼成一条**完整直播链路**,全程不转码:

```
推流端(OBS/ffmpeg) ──RTMP──▶ pkg/media/rtmp ──pkg/media/remux(FLV→MPEG-TS)──▶ pkg/hls ──HTTP──▶ 播放端
```

- **采集** [`pkg/media/rtmp`](../../pkg/media/rtmp):收 OBS/ffmpeg 推流,拿到 FLV 音视频;支持连接级(`WithConnectAuth`)与推流级(`PublishFunc` 返回 nil 拒绝)鉴权。
- **remux** [`pkg/media/remux`](../../pkg/media/remux):FLV(H.264/AAC)→ MPEG-TS,按关键帧切片,**不转码**。
- **分发** [`pkg/hls`](../../pkg/hls):滚动分片窗口 + m3u8,挂 `webserver`;分片存储可插拔(内存/磁盘/对象存储)。

## 运行

```bash
go run ./examples/live-hls
# RTMP ingest :1935 | HLS play http://localhost:8090/live/index.m3u8
```

推流(需要 ffmpeg;`input.mp4` 换成你的文件,或用摄像头):

```bash
ffmpeg -re -stream_loop -1 -i input.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/demo
```

播放:

```bash
ffplay http://localhost:8090/live/index.m3u8
# 或用支持 HLS 的浏览器 / Safari 打开该地址
```

## 说明与限制

- 只处理 **H.264 + AAC**(OBS/ffmpeg 默认);其它编码会被 remux 丢弃。
- 分片在**视频关键帧**处切,实际时长 ≈ max(2s, 推流端 GOP);OBS 里把关键帧间隔设小可降低延迟。
- 本示例把所有推流都汇入**同一路** `stream`;多主播时应按 `streamKey` 维护多路 `hls.Stream`。
- remux 的解析/切片有单元测试,但**真机播放器互操作请自行用 ffplay/Safari 验证**后再上生产。
- 延迟、弱网、多码率等生产话题见项目讨论(本链路是标准 HLS,秒级延迟;要更低延迟需 LL-HLS 或 WebRTC)。
