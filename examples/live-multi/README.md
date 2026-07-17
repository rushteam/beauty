# live-multi —— 多路直播 + 多路流管理(Hub)+ OTel 指标

一个 RTMP 端口收**多路**流,按 `streamKey` 各自成流、各自分发,用
[`pkg/media.Hub`](../../pkg/media) 做注册表/生命周期/路由 + OTel 运维指标。走 copy 路径
(remux,不转码)。

```
推 /live/roomA ─┐
推 /live/roomB ─┼─▶ rtmp.Server ─▶ Hub.Acquire(key) ─▶ remux → 各自 hls.Stream
                ┘                         │
              播放 /live/roomA/index.m3u8 ◀── Hub 按 key 路由
```

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
(RTMP URL、文件、FIFO)的拓扑——崩了重启能自动重连。而本示例这种把 FLV **管道喂给
ffmpeg stdin** 的方式,进程一旦崩,输入管道即断,正确做法是**结束本次推流、等推流端重推**
(而非原地重启)。所以本示例走 copy 路径不涉及转码进程;要 stdin 管道转码见
[`examples/live-transcode`](../live-transcode),要带重启的转码请用"拉流/读文件"式输入。

## 边界

- 转码档位、ffmpeg 参数、导出器(Prometheus/OTLP)、鉴权 token 体系 —— 都是 policy,不在框架内。
- 多副本部署要共享存储(`hls.WithStore` 接对象存储)或按 key 粘连路由;CDN 在框架外回源。
