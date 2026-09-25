# Media Real-Device Validation Checklist

> 中文版: [media-validation.md](media-validation.md)

Run the media pipeline end to end with real encoded streams in real players. Below are copy-pasteable commands and checkpoints.

## 0. Prerequisites

```bash
# requires ffmpeg / ffplay (or VLC), plus a test source (or synthesize one with ffmpeg)
ffmpeg -version

# without a source file, use a synthetic source (color bars + sine tone); the commands below refer to it as $SRC:
# real-time synthesis:
SRC='-re -f lavfi -i testsrc=size=1280x720:rate=30 -f lavfi -i sine=frequency=1000'
# or use a file: SRC='-re -stream_loop -1 -i input.mp4'
```

Always use the **H.264 + AAC** codec combination (the framework's RTMP/hlsmux only bridges this combination, which is also the OBS/ffmpeg default):
`-c:v libx264 -c:a aac`.

---

## 1. RTMP → LL-HLS (pkg/media/hlsmux, single stream)

```bash
go run ./examples/live-hls-gohlslib      # starts RTMP :1935 + HLS player page :8080
# publish from another terminal:
ffmpeg $SRC -c:v libx264 -preset veryfast -tune zerolatency \
  -c:a aac -f flv rtmp://localhost:1935/live/stream
```

Checkpoints:

- [ ] **Browser**: open <http://localhost:8080/>; video starts playing with audio (hls.js; Safari uses native HLS).
- [ ] **Command-line player**: `ffplay http://localhost:8080/hls/index.m3u8` (or open the URL in VLC) plays.
- [ ] **Valid playlist**: the first line of `curl -s http://localhost:8080/hls/index.m3u8` is `#EXTM3U`;
      LL-HLS should include `#EXT-X-PART` / `#EXT-X-SERVER-CONTROL`.
- [ ] **A/V sync**: lip movement/action and audio do not drift (watch for ≥1 minute).
- [ ] **Latency**: overlay a timecode on the source with `-vf "drawtext=text='%{localtime}'..."` and compare it with the time shown on the player;
      the LL-HLS target is 1–3s, regular HLS is several seconds.
- [ ] **Recovery after stream drop**: Ctrl-C to stop ffmpeg and then republish; the player recovers (or reports a clear error rather than hanging).

---

## 2. Multi-Stream + Hub + Metrics (pkg/media.Hub, examples/live-multi)

```bash
go run ./examples/live-multi             # RTMP :1935 + :8090
# publish two streams with different streamKeys at the same time:
ffmpeg $SRC -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/roomA &
ffmpeg $SRC -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/roomB &
```

Checkpoints:

- [ ] `ffplay http://localhost:8090/live/roomA/index.m3u8` and `.../roomB/...` **do not cross streams**.
- [ ] `curl http://localhost:8090/streams` returns `{"active":2}`; it becomes 1 after stopping one stream.
- [ ] **Duplicate-publish protection**: publishing another stream to `roomA` while it is already live is rejected on the second attempt (the publisher gets an error and the original stream is unaffected).
- [ ] With an OTel exporter configured, `media.streams.active` / `media.ingest.bytes` report data.

---

## 3. WHIP/WHEP (pkg/media/webrtc, sub-second)

```bash
go run ./examples/webrtc-whip-whep       # :8080
```

Checkpoints:

- [ ] Open <http://localhost:8080/publish> in a browser, allow camera/microphone, and see the local preview.
- [ ] Open <http://localhost:8080/> in another tab (multiple tabs allowed) to watch; the publisher's video + audio appears with **sub-second** latency.
- [ ] Multiple viewers online at the same time can all play (one publisher, many viewers).
- [ ] Publishing via **OBS WHIP** (choose WHIP as the service, URL `http://localhost:8080/whip/live`) can also be played in the web page.
- [ ] After closing the publisher, the viewers' tracks end (no endless spinner).
- [ ] For cross-machine/cross-network tests, if the connection fails → STUN/TURN is required (`webrtc.WithICEServers`); it can be omitted on the same machine/subnet.

---

## 4. Multi-Party Voice Conference (pkg/media/webrtc/sfu, examples/webrtc-voice-room)

```bash
go run ./examples/webrtc-voice-room      # :8080
```

Checkpoints:

- [ ] Open ≥3 browser tabs and click "Join" in each; **every pair can hear each other**.
- [ ] **Dynamic join/leave**: when a 3rd person joins midway, the two already in the room can hear the newcomer; when someone leaves, their audio track disappears for everyone else.
- [ ] The connection state shows `connected`; after network jitter it recovers on its own or disconnects cleanly.
- [ ] Echo: use headphones to avoid speaker feedback (echo cancellation happens on the browser `getUserMedia` side, not the server).

---

## 5. Multi-Replica Sharding (pkg/store/shard, optional, validates horizontal scaling)

Start one live-multi instance on each of two ports (or wrap them with a `shard.Router`), and point `shard.SetMembers` at both instances:

- [ ] Publish `roomX` to instance A, request `/live/roomX/index.m3u8` from **instance B**; B **reverse-proxies** to A and plays it.
- [ ] After the owning instance goes down, `SetMembers` updates the member set and ownership of `roomX` migrates (new streams can land on surviving instances).

---

## General Red Lines (do not ship if any fails)

- [ ] At least **Safari (native HLS) + Chrome (hls.js) + ffplay/VLC** can all play HLS.
- [ ] Runs continuously for **≥30 minutes** with no steady memory growth and no crashes (check heap with `pprof`).
- [ ] Under a weak network (simulate packet loss with `tc`/Network Link Conditioner), playback degrades rather than freezing.
- [ ] Publishing/dropping the stream 100 times repeatedly causes no fd/goroutine leaks (goroutine count in `pprof` stays stable).
