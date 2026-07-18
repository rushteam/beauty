// webrtc-whip-whep demo:最小 SFU 广播——一路 WHIP 推流,转发给任意多个 WHEP 播放端。
//
//	浏览器/OBS(推 /whip/live)─▶ WHIP.OnTrack ─▶ Pipe(RTP 转发)─▶ 每路 local track
//	                                                                  │
//	           多个浏览器(播 /whep/live)◀── WHEP.AddTrack 订阅同一组 local track
//
// 用 pkg/media/webrtc 的薄机制:我们只负责 SDP/ICE 协商与 RTP 转发;转发拓扑(这里是
// 「一推多播」的最小 SFU)是本示例的 policy,写在下面的 broadcast 里。
//
// 运行:
//
//	go run ./examples/webrtc-whip-whep
//	# 浏览器打开 http://localhost:8080/publish 用摄像头推流(WHIP)
//	# 另开 http://localhost:8080/ 观看(WHEP);可多开几个标签页验证一推多播
//
// 也可用 OBS(设置 → 直播 → 服务「WHIP」,推流地址 http://localhost:8080/whip/live)推流。
//
// 边界:延迟档位是亚秒级(WebRTC),和 pkg/hls 的多秒级分发互补。鉴权、STUN/TURN、
// 转码、录制都不在这里。晚到的轨道不做重协商(播放端连上之后新增的轨道不会补推)。
package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/media/webrtc"
	"github.com/rushteam/beauty/pkg/service/webserver"

	pion "github.com/pion/webrtc/v4"
)

// broadcast 是「一推多播」的转发状态(policy):每个 streamKey 保存一组可写本地轨道,
// WHIP 收到的每路轨道转发进来、每个 WHEP 播放端订阅出去。
type broadcast struct {
	mu     sync.Mutex
	tracks map[string][]*pion.TrackLocalStaticRTP
}

func newBroadcast() *broadcast {
	return &broadcast{tracks: make(map[string][]*pion.TrackLocalStaticRTP)}
}

func (b *broadcast) add(key string, t *pion.TrackLocalStaticRTP) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tracks[key] = append(b.tracks[key], t)
}

func (b *broadcast) get(key string) []*pion.TrackLocalStaticRTP {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*pion.TrackLocalStaticRTP(nil), b.tracks[key]...)
}

func main() {
	api, err := webrtc.NewAPI()
	if err != nil {
		panic(err)
	}
	bc := newBroadcast()

	// WHIP:收流。每路进来的轨道 → 建一个同编解码的本地轨道 → Pipe 转发 → 存入 broadcast。
	whip := webrtc.NewWHIP(api, func(streamKey string, pc *pion.PeerConnection) error {
		pc.OnTrack(func(remote *pion.TrackRemote, _ *pion.RTPReceiver) {
			local, err := webrtc.NewLocalTrackFor(remote, remote.Kind().String(), streamKey)
			if err != nil {
				return
			}
			bc.add(streamKey, local)
			_ = webrtc.Pipe(context.Background(), remote, local) // 转发到停,阻塞在此 goroutine
		})
		return nil
	}, webrtc.WithBasePath("/whip"))

	// WHEP:发流。把该 streamKey 当前的所有本地轨道挂给这个播放端。
	whep := webrtc.NewWHEP(api, func(streamKey string, pc *pion.PeerConnection) error {
		for _, t := range bc.get(streamKey) {
			if _, err := pc.AddTrack(t); err != nil {
				return err
			}
		}
		return nil
	}, webrtc.WithBasePath("/whep"))

	mux := http.NewServeMux()
	mux.Handle("/whip/", whip)
	mux.Handle("/whep/", whep)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(viewerHTML))
	})
	mux.HandleFunc("/publish", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(publisherHTML))
	})

	app := beauty.New(
		beauty.WithWebServer(":8080", mux, webserver.WithServiceName("webrtc-sfu")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

// ---- 浏览器端 policy(纯前端,和框架无关);非 trickle:等 ICE 收集完再 POST ----

const negotiateJS = `
async function gather(pc){ if(pc.iceGatheringState==='complete')return;
  await new Promise(r=>{pc.addEventListener('icegatheringstatechange',()=>{if(pc.iceGatheringState==='complete')r();});}); }
async function exchange(pc, url){
  const offer = await pc.createOffer(); await pc.setLocalDescription(offer); await gather(pc);
  const res = await fetch(url,{method:'POST',headers:{'Content-Type':'application/sdp'},body:pc.localDescription.sdp});
  if(!res.ok){ throw new Error('negotiate failed: '+res.status); }
  await pc.setRemoteDescription({type:'answer', sdp: await res.text()});
}`

const viewerHTML = `<!doctype html><meta charset=utf-8><title>WHEP viewer</title>
<h3>WHEP 播放 (/whep/live)</h3><video id=v autoplay playsinline controls muted style="width:640px;background:#000"></video>
<p><a href="/publish">去推流 →</a></p>
<script>` + negotiateJS + `
(async()=>{ const pc=new RTCPeerConnection();
  pc.addTransceiver('video',{direction:'recvonly'}); pc.addTransceiver('audio',{direction:'recvonly'});
  pc.ontrack=e=>{ document.getElementById('v').srcObject=e.streams[0]; };
  try{ await exchange(pc,'/whep/live'); }catch(err){ document.body.append(' '+err); }
})();
</script>`

const publisherHTML = `<!doctype html><meta charset=utf-8><title>WHIP publisher</title>
<h3>WHIP 推流 (/whip/live)</h3><video id=v autoplay playsinline muted style="width:640px;background:#000"></video>
<p><a href="/">← 去观看</a></p>
<script>` + negotiateJS + `
(async()=>{ const stream=await navigator.mediaDevices.getUserMedia({video:true,audio:true});
  document.getElementById('v').srcObject=stream;
  const pc=new RTCPeerConnection(); stream.getTracks().forEach(t=>pc.addTrack(t,stream));
  try{ await exchange(pc,'/whip/live'); }catch(err){ document.body.append(' '+err); }
})();
</script>`
