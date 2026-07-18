// webrtc-voice-room demo:多人实时语音(SFU 会议室)。每个浏览器推自己的麦克风、订阅
// 其他所有人的音频,服务端做选择性转发(不混流不转码)。用 WebSocket(pkg/ws)承载信令,
// 服务端在成员进出时重协商——这正是 WHIP/WHEP 覆盖不到的「N↔N 动态成员」那一档。
//
//	浏览器A ─┐  WS 信令(offer/answer/candidate)   ┌─ 服务端 sfu.Room
//	浏览器B ─┼───────────────────────────────────┤  · 收每个人的音轨,转发给其他人
//	浏览器C ─┘                                     └─ 成员进出 → 服务端发起重协商
//
// 运行:
//
//	go run ./examples/webrtc-voice-room
//	# 多个浏览器/标签页打开 http://localhost:8080/,点「加入」即可互相通话
//
// 边界(机制而非策略):鉴权、房间划分(这里是单一全局房间)、只音频还是带视频、
// 混流/主讲人检测、STUN/TURN、录制都不在这里。跨 NAT 需要在 sfu.WithICEServers 配 TURN。
package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/media/webrtc"
	"github.com/rushteam/beauty/pkg/media/webrtc/sfu"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/ws"
)

func main() {
	api, err := webrtc.NewAPI()
	if err != nil {
		panic(err)
	}
	room := sfu.NewRoom(api, sfu.WithName("voice"))

	// WebSocket 信令端点:一条连接 = 一个参会者。
	signal := ws.Handler(func(r *http.Request, c *ws.Conn) error {
		id := r.URL.Query().Get("id")
		if id == "" {
			id = r.RemoteAddr // 兜底;正式场景应由鉴权给出稳定 id
		}
		ctx := r.Context()

		// 写要串行化(pion 的 ICE/协商回调会从多个 goroutine 触发 send)。
		var writeMu sync.Mutex
		send := func(m sfu.Message) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return c.WriteJSON(wctx, m)
		}

		p, err := room.Join(id, send)
		if err != nil {
			return err
		}
		defer p.Close() // 连接断开即离开房间,触发其他人重协商

		for {
			var m sfu.Message
			if err := c.ReadJSON(ctx, &m); err != nil {
				return nil // 连接关闭
			}
			if err := p.HandleSignal(m); err != nil {
				slog.Debug("voice-room: handle signal", "peer", id, "event", m.Event, "err", err)
			}
		}
	}, ws.WithOriginPatterns("*")) // demo 放开来源;生产按需收紧

	mux := http.NewServeMux()
	mux.Handle("/ws", signal)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(pageHTML))
	})

	app := beauty.New(
		beauty.WithWebServer(":8080", mux, webserver.WithServiceName("voice-room")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

// 浏览器端 policy(纯前端):加入时 getUserMedia(仅音频)+ 一次初始 offer;之后应答
// 服务端重协商的 offer,双向 trickle 候选;每个远端音轨挂一个 <audio autoplay>。
const pageHTML = `<!doctype html><meta charset=utf-8><title>Voice Room</title>
<h3>多人语音会议室</h3>
<button id=join>加入</button> <span id=status>未加入</span>
<div id=peers></div>
<script>
const $=id=>document.getElementById(id);
$('join').onclick=async()=>{
  $('join').disabled=true; $('status').textContent='获取麦克风…';
  let stream;
  try{ stream=await navigator.mediaDevices.getUserMedia({audio:true}); }
  catch(e){ $('status').textContent='麦克风被拒: '+e; $('join').disabled=false; return; }

  const id=(crypto.randomUUID?crypto.randomUUID():String(Math.random())).slice(0,8);
  const ws=new WebSocket((location.protocol==='https:'?'wss':'ws')+'://'+location.host+'/ws?id='+id);
  const pc=new RTCPeerConnection();
  let remoteDesc=false; const pend=[];

  stream.getTracks().forEach(t=>pc.addTrack(t,stream));
  pc.onicecandidate=e=>{ if(e.candidate) ws.send(JSON.stringify({event:'candidate',data:e.candidate})); };
  pc.ontrack=e=>{
    const el=document.createElement('audio'); el.autoplay=true; el.controls=true;
    el.srcObject=e.streams[0]||new MediaStream([e.track]);
    const box=document.createElement('div'); box.textContent='remote track '+e.track.id.slice(0,6)+' ';
    box.appendChild(el); $('peers').appendChild(box);
    e.track.onended=()=>box.remove();
  };
  pc.onconnectionstatechange=()=>{ $('status').textContent='连接: '+pc.connectionState; };

  async function flush(){ remoteDesc=true; while(pend.length){ try{await pc.addIceCandidate(pend.shift());}catch(_){}} }
  ws.onmessage=async ev=>{
    const m=JSON.parse(ev.data);
    if(m.event==='offer'){ // 服务端重协商
      await pc.setRemoteDescription({type:'offer',sdp:m.data}); await flush();
      const ans=await pc.createAnswer(); await pc.setLocalDescription(ans);
      ws.send(JSON.stringify({event:'answer',data:pc.localDescription.sdp}));
    }else if(m.event==='answer'){ await pc.setRemoteDescription({type:'answer',sdp:m.data}); await flush(); }
    else if(m.event==='candidate'){ if(remoteDesc){ try{await pc.addIceCandidate(m.data);}catch(_){} } else pend.push(m.data); }
  };
  ws.onopen=async()=>{ // 客户端只发这一次初始 offer
    const offer=await pc.createOffer(); await pc.setLocalDescription(offer);
    ws.send(JSON.stringify({event:'offer',data:pc.localDescription.sdp}));
    $('status').textContent='已加入 ('+id+')';
  };
  ws.onclose=()=>{ $('status').textContent='已断开'; };
};
</script>`
