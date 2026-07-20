# 媒体真机验证清单

用真实编码流在真播放器里端到端跑一遍媒体链路。以下为可照抄的命令与检查点。

## 0. 前置

```bash
# 需要 ffmpeg / ffplay(或 VLC),以及一个测试片源(或用 ffmpeg 合成)
ffmpeg -version

# 没有片源就用合成源(彩条 + 正弦音),下文命令用 $SRC 代指:
# 实时合成:
SRC='-re -f lavfi -i testsrc=size=1280x720:rate=30 -f lavfi -i sine=frequency=1000'
# 或用文件:SRC='-re -stream_loop -1 -i input.mp4'
```

编码组合固定用 **H.264 + AAC**(框架 RTMP/hlsmux 只桥接这一组,也是 OBS/ffmpeg 默认):
`-c:v libx264 -c:a aac`。

---

## 1. RTMP → LL-HLS(pkg/media/hlsmux,单路)

```bash
go run ./examples/live-hls-gohlslib      # 起 RTMP :1935 + HLS 播放页 :8080
# 另开终端推流:
ffmpeg $SRC -c:v libx264 -preset veryfast -tune zerolatency \
  -c:a aac -f flv rtmp://localhost:1935/live/stream
```

检查点:

- [ ] **浏览器**打开 <http://localhost:8080/>,视频能起播、有声音(hls.js;Safari 走原生 HLS)。
- [ ] **命令行播放器**:`ffplay http://localhost:8080/hls/index.m3u8`(或 VLC 打开该 URL)能放。
- [ ] **播放列表合法**:`curl -s http://localhost:8080/hls/index.m3u8` 首行是 `#EXTM3U`;
      LL-HLS 应含 `#EXT-X-PART` / `#EXT-X-SERVER-CONTROL`。
- [ ] **A/V 同步**:口型/动作与声音不漂移(持续看 ≥1 分钟)。
- [ ] **延迟**:在源上叠时间码 `-vf "drawtext=text='%{localtime}'..."`,对比播放端显示时间;
      LL-HLS 目标 1–3s,普通 HLS 数秒级。
- [ ] **断流恢复**:Ctrl-C 停 ffmpeg 再重推,播放端能恢复(或明确报错,不卡死)。

---

## 2. 多路 + Hub + 指标(pkg/media.Hub,examples/live-multi)

```bash
go run ./examples/live-multi             # RTMP :1935 + :8090
# 同时推两路不同 streamKey:
ffmpeg $SRC -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/roomA &
ffmpeg $SRC -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/roomB &
```

检查点:

- [ ] `ffplay http://localhost:8090/live/roomA/index.m3u8` 与 `.../roomB/...` **互不串流**。
- [ ] `curl http://localhost:8090/streams` 返回 `{"active":2}`;停一路后变 1。
- [ ] **防抢流**:对已在推的 `roomA` 再推一路,第二次被拒(推流端报错、原流不受影响)。
- [ ] 配了 OTel 导出器时,`media.streams.active` / `media.ingest.bytes` 有数据。

---

## 3. WHIP/WHEP(pkg/media/webrtc,亚秒级)

```bash
go run ./examples/webrtc-whip-whep       # :8080
```

检查点:

- [ ] 浏览器开 <http://localhost:8080/publish>,允许摄像头/麦克风,能看到本地画面。
- [ ] 另开(可多开)<http://localhost:8080/> 观看,**亚秒级**看到推流端画面 + 声音。
- [ ] 多个观看端同时在线,都能播(一推多播)。
- [ ] 用 **OBS 的 WHIP**(服务选 WHIP,地址 `http://localhost:8080/whip/live`)推流也能被网页播放。
- [ ] 关掉推流端,观看端轨道结束(不无限转圈)。
- [ ] 跨机/跨网测试时,若连不上→需要 STUN/TURN(`webrtc.WithICEServers`);本机/同网段可省。

---

## 4. 多人语音会议(pkg/media/webrtc/sfu,examples/webrtc-voice-room)

```bash
go run ./examples/webrtc-voice-room      # :8080
```

检查点:

- [ ] 开 ≥3 个浏览器标签,各点「加入」,**两两都能听到**对方。
- [ ] **动态进出**:第 3 人中途加入,已在房间的两人能听到新人;有人离开,其音轨从其他人处消失。
- [ ] 连接状态显示 `connected`;网络抖动后能自恢复或明确断开。
- [ ] 回声:用耳机避免外放啸叫(回声消除是浏览器 `getUserMedia` 侧,非服务端)。

---

## 5. 多副本分片(pkg/shard,可选,验证水平扩展)

在两个端口各起一份 live-multi(或包一层 `shard.Router`),把 `shard.SetMembers` 指向两个实例:

- [ ] 推到实例 A 的 `roomX`,从**实例 B** 请求 `/live/roomX/index.m3u8`,B 能**反代**到 A 并放出。
- [ ] 归属实例宕机后,`SetMembers` 更新成员集,`roomX` 的归属迁移(新流可落到存活实例)。

---

## 通用红线(任一不过就别上)

- [ ] 至少 **Safari(原生 HLS)+ Chrome(hls.js)+ ffplay/VLC** 三者都能放 HLS。
- [ ] 连续跑 **≥30 分钟**无内存持续上涨、无崩溃(`pprof` 看 heap)。
- [ ] 弱网(用 `tc`/Network Link Conditioner 模拟丢包)下播放降级而非卡死。
- [ ] 反复推流/断流 100 次无 fd/goroutine 泄漏(`pprof` goroutine 数稳定)。
