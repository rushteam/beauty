# webrtc-whip-whep —— WebRTC 最小 SFU(WHIP 推 / WHEP 播,一推多播)

亚秒级实时媒体:一路 **WHIP** 推流,经服务端 **RTP 转发**广播给任意多个 **WHEP** 播放端。
基于 [`pkg/media/webrtc`](../../pkg/media/webrtc)(纯 Go pion,零 cgo)。

```
浏览器/OBS(推 /whip/live) ─▶ WHIP.OnTrack ─▶ webrtc.Pipe(RTP 纯转发) ─▶ 每路 local track
                                                                          │
             多个浏览器(播 /whep/live) ◀── WHEP.AddTrack 订阅同一组 local track
```

和 HLS 的关系:这是**亚秒级、交互式**那一档(连麦/云游戏/实时协作);`pkg/hls` 是
**多秒级、可过 CDN** 的分发那一档。二者互补,按延迟需求选。

## 运行

```bash
go run ./examples/webrtc-whip-whep
```

- 推流:浏览器打开 <http://localhost:8080/publish>(用摄像头/麦克风,WHIP);
  或用 **OBS**(设置 → 直播 → 服务选「WHIP」,推流地址 `http://localhost:8080/whip/live`)。
- 观看:打开 <http://localhost:8080/>(WHEP)。多开几个标签页即可验证一推多播。

> 局域网/本机直接用主机候选即可连通;跨 NAT 需要 STUN/TURN,见下。

## 这个包给了什么(机制),什么留给你(策略)

**机制(pkg/media/webrtc)**:

- `NewWHIP` / `NewWHEP`:WHIP/WHEP 的 `http.Handler`,做 SDP/ICE 协商(服务端 answerer)
  + 资源生命周期(`Location` + `DELETE` + 断连自动回收)。挂 `beauty.WithWebServer` 上即可。
- `Pipe(remote, local)`:把收到的 RTP 包原样转发到本地轨道,纯转发不转码——SFU 的最小原语。
- `NewLocalTrackFor(remote,…)`:按远端编解码建可写本地轨道。

**策略(本示例的 `main.go` 里)**:

- **转发拓扑**:这里是「一推多播」最小 SFU(一个 `streamKey` → 一组共享 local track)。
  多人会议(多推多播)、选择性转发(按订阅/大小流)都在这一层自定义。
- **鉴权**:在 `NewWHIP`/`NewWHEP` 的回调里按 `streamKey`/token 校验,返回 error 即 403。
- **NAT 穿透**:`webrtc.WithICEServers(...)` 配 STUN/TURN。
- **编解码档位**:`webrtc.WithMediaEngine(...)` 自定义(如只留 H264+Opus)。
- **CORS**:浏览器跨源播放需要,自己在 mux 上加中间件(本包只对 `OPTIONS` 回 204)。

## 已知边界

- **不重协商**:播放端连上后,发布端新增的轨道不会补推(demo 简化)。
- **不转码**:纯 RTP 转发。要转码/混流(MCU)在外面接 ffmpeg 或独立媒体处理。
- **单副本**:共享 track 存在进程内存里;多副本部署要把「谁在推哪路」放到共享层并路由。
