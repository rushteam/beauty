# live-multi —— 多路直播 + 多路流管理(Hub)+ OTel 指标

一个 RTMP 端口收**多路**流,按 `streamKey` 各自成流、各自分发,用
[`pkg/media.Hub`](../../pkg/media) 做注册表/生命周期/路由 + OTel 运维指标。每路 HLS 由
[`pkg/media/hlsmux`](../../pkg/media/hlsmux)(gohlslib)产出 LL-HLS,不转码。

```
推 /live/roomA ─┐
推 /live/roomB ─┼─▶ rtmp.Server ─▶ Hub.Acquire(key) ─▶ hlsmux.Bridge(→LL-HLS)
                ┘                         │
              播放 /live/roomA/index.m3u8 ◀── Hub 按 key 路由
```

`Hub` 现在是泛型 `Hub[S media.Stream]`(`Stream = http.Handler + Finish()`),这里的 `S` 是
`*hlsmux.Bridge`;换成 `*hls.Stream` 即回到自研 origin。`Bridge` 同时是 `rtmp.Handler`(收流)
与 `http.Handler`(播放),所以一个对象既进 `Acquire` 收流、又被 Hub 路由分发。

Hub 提供的薄机制:

- **多路管理**:`Acquire(key)` 抢占一路(重复 key 被拒,防抢流)、`Release(key)` 回收、`Count()`、`Lookup()`。
- **路由**:`Hub` 实现 `http.Handler`,`/{key}/index.m3u8`、`/{key}/segN.ts` 自动路由到该路 `Stream`。
- **生命周期**:`Session.Context()` 在 `Release` 时取消——把 ffmpeg `Supervisor` 等后台任务绑上去即随流停机。
- **OTel 指标**:`media.streams.active`、`media.publish.total`、`media.publish.rejected`、`media.ingest.bytes` 等,走全局 MeterProvider,配了 telemetry 才导出,否则 no-op。

## 运行

```bash
go run ./examples/live-multi
# 推两路(不同 key):
ffmpeg -re -stream_loop -1 -i a.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/roomA
ffmpeg -re -stream_loop -1 -i b.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/roomB
# 播放 / 看在线流:
ffplay http://localhost:8090/live/roomA/index.m3u8
curl   http://localhost:8090/streams
```

## Supervisor(转码进程崩溃重启)怎么配

`pkg/media.Supervisor` 是通用的子进程监督(启动/退避重启/优雅停),把它绑到
`Session.Context()` 即随该路流停机:

```go
sess, _ := hub.Acquire(key)
sup := media.NewSupervisor(
    func() *exec.Cmd { return exec.Command("ffmpeg", args...) }, // 命令构造是 policy
    media.WithSupervisorMetrics(hub.Metrics(), key),             // 重启计入 media.transcode.restarts
)
go sup.Run(sess.Context()) // Release 时自动停
```

**适用边界(重要)**:Supervisor 的"崩溃重启"适合 ffmpeg **从可重连的源拉流/读稳定输入**
(RTMP URL、文件、FIFO)的拓扑——崩了重启能自动重连。而把 FLV **管道喂给 ffmpeg stdin**
的方式,进程一旦崩、输入管道即断,正确做法是**结束本次推流、等推流端重推**(而非原地重启)。
本示例走不转码路径(gohlslib 直接封装)不涉及转码进程;要 stdin 管道转码见
[`examples/live-transcode`](../live-transcode),要带重启的转码请用"拉流/读文件"式输入。

## 边界

- 转码档位、ffmpeg 参数、导出器(Prometheus/OTLP)、鉴权 token 体系 —— 都是 policy,不在框架内。
- 存储:gohlslib 分片默认在内存,`hlsmux.WithDirectory` 可落盘给 CDN 回源(注:LL-HLS variant
  不支持落盘)。要**可插拔对象存储**(`hls.WithStore`)得用 `*hls.Stream` 那条路径——把 Hub 的
  `S` 换成 `*hls.Stream` 并自行喂分片。多副本部署另需按 key 粘连路由。
