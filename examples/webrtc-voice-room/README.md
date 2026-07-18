# webrtc-voice-room —— 多人实时语音(SFU 会议室)

N 个人互相通话:每个浏览器推自己的麦克风、订阅其他所有人的音频,服务端做**选择性转发**
(SFU,不混流不转码)。基于 [`pkg/media/webrtc/sfu`](../../pkg/media/webrtc/sfu),信令走
WebSocket([`pkg/ws`](../../pkg/ws))。

```
浏览器A ─┐  WS 信令(offer/answer/candidate)   ┌─ 服务端 sfu.Room
浏览器B ─┼───────────────────────────────────┤  · 收每个人的音轨,Pipe 转发给其他人
浏览器C ─┘                                     └─ 成员进出 → 服务端发起重协商
```

这是 WHIP/WHEP([examples/webrtc-whip-whep](../webrtc-whip-whep))覆盖不到的一档:WHIP/WHEP
是「单路进/单路出」,不处理动态成员;会议室是 **N↔N 且随时进出**,所以需要一条持久信令
通道 + 服务端重协商。

## 运行

```bash
go run ./examples/webrtc-voice-room
# 多个浏览器 / 标签页打开 http://localhost:8080/,各自点「加入」即可互相通话
```

> 局域网/本机用主机候选即可连通;跨 NAT 要在 `sfu.WithICEServers(...)` 配 STUN/TURN。
> 点「加入」是必要的用户手势——否则浏览器会拦截带声音的自动播放。

## 无 glare 的重协商模型

多人会议最难的是「谁来发 offer」。本示例(与 `pkg/media/webrtc/sfu`)采用固定分工:

- **客户端只在加入时发一次 offer**(携带自己要推的音轨);
- **此后所有重协商都由服务端作为 offerer 发起**——有人进/出导致轨道增减时,服务端
  `CreateOffer` 推给对应客户端,客户端只负责 answer。

客户端永不二次 offer,从根上避免了双方同时 offer 的碰撞(glare)。服务端的 `resync`
在信令未落定(非 stable)时自动推迟并在 answer 到达后重试。

## 机制 vs 策略

**机制(pkg/media/webrtc/sfu)**:房间成员管理、轨道扇出转发、服务端重协商、trickle ICE。
`Room.Join` 返回的 `Participant` 只暴露 `HandleSignal`(喂客户端来的信令)与 `Close`(离开)。

**策略(本示例)**:

- **信令承载**:这里用 `pkg/ws`;换成别的传输只要实现「收到消息 → `HandleSignal`,
  `Room` 回调 `send` → 发出去」即可。
- **房间划分**:demo 是单一全局房间;多房间按 `?room=` 各建一个 `sfu.Room` 路由。
- **参会者 id / 鉴权**:demo 用 URL 里的随机 id;正式场景应由鉴权签发稳定 id。
- **音频 vs 视频**:`Room` 转发任意轨道;本 demo 只 `getUserMedia({audio:true})`。带视频
  把前端改成 `{audio:true,video:true}` 即可(服务端不用改)。
- **STUN/TURN、混流(MCU)、主讲人检测、录制**:都在这一层之外。

## 边界

- 纯转发不转码;要混流成单轨(省带宽/兼容电话网关)得在外面接 MCU。
- 单副本:房间与轨道在进程内存里;多副本要把成员/轨道放到共享层并做级联转发。
