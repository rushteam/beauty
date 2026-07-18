package sfu_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	pion "github.com/pion/webrtc/v4"

	"github.com/rushteam/beauty/pkg/media/webrtc"
	"github.com/rushteam/beauty/pkg/media/webrtc/sfu"
)

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// clientPeer 在测试里模拟浏览器一侧:客户端只发一次初始 offer,之后应答服务端的重协商
// offer,并双向 trickle 候选。收到任一远端音轨的 RTP 即视为「听到」了别人。
type clientPeer struct {
	t        *testing.T
	pc       *pion.PeerConnection
	toServer func(sfu.Message) // 发给服务端(异步)

	mu        sync.Mutex
	pending   []pion.ICECandidateInit
	gotAudio  chan struct{}
	audioOnce sync.Once
}

func (c *clientPeer) handle(m sfu.Message) {
	switch m.Event {
	case sfu.EventOffer: // 服务端发起的重协商
		var sdp string
		_ = json.Unmarshal(m.Data, &sdp)
		if err := c.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeOffer, SDP: sdp}); err != nil {
			return
		}
		c.flush()
		ans, err := c.pc.CreateAnswer(nil)
		if err != nil {
			return
		}
		if err := c.pc.SetLocalDescription(ans); err != nil {
			return
		}
		c.toServer(sfu.Message{Event: sfu.EventAnswer, Data: mustJSON(ans.SDP)})
	case sfu.EventAnswer:
		var sdp string
		_ = json.Unmarshal(m.Data, &sdp)
		_ = c.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: sdp})
		c.flush()
	case sfu.EventCandidate:
		var cand pion.ICECandidateInit
		_ = json.Unmarshal(m.Data, &cand)
		c.mu.Lock()
		if c.pc.RemoteDescription() == nil {
			c.pending = append(c.pending, cand)
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		_ = c.pc.AddICECandidate(cand)
	}
}

func (c *clientPeer) flush() {
	c.mu.Lock()
	pend := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, cand := range pend {
		_ = c.pc.AddICECandidate(cand)
	}
}

// addClient 建一个客户端 PC,加入 room,推一路 Opus 音轨并持续写 RTP。
func addClient(t *testing.T, room *sfu.Room, api *pion.API, id string, stop <-chan struct{}) *clientPeer {
	t.Helper()
	pc, err := api.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatalf("client %s pc: %v", id, err)
	}
	c := &clientPeer{t: t, pc: pc, gotAudio: make(chan struct{})}

	// 收到任一远端轨道的 RTP 即认为听到了别人。
	pc.OnTrack(func(remote *pion.TrackRemote, _ *pion.RTPReceiver) {
		for {
			if _, _, err := remote.ReadRTP(); err != nil {
				return
			}
			c.audioOnce.Do(func() { close(c.gotAudio) })
		}
	})

	// 服务端 → 客户端。
	send := func(m sfu.Message) error {
		go c.handle(m)
		return nil
	}
	p, err := room.Join(id, send)
	if err != nil {
		t.Fatalf("join %s: %v", id, err)
	}
	c.toServer = func(m sfu.Message) { go func() { _ = p.HandleSignal(m) }() }

	// 客户端 → 服务端 trickle 候选。
	pc.OnICECandidate(func(cand *pion.ICECandidate) {
		if cand == nil {
			return
		}
		c.toServer(sfu.Message{Event: sfu.EventCandidate, Data: mustJSON(cand.ToJSON())})
	})

	// 推一路 Opus 音轨。
	track, err := pion.NewTrackLocalStaticRTP(
		pion.RTPCodecCapability{MimeType: pion.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio-"+id, id,
	)
	if err != nil {
		t.Fatalf("client %s track: %v", id, err)
	}
	if _, err := pc.AddTrack(track); err != nil {
		t.Fatalf("client %s add track: %v", id, err)
	}
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		var seq uint16
		var ts uint32
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				seq++
				ts += 960
				_ = track.WriteRTP(&rtp.Packet{
					Header:  rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: ts, SSRC: 0x5678},
					Payload: []byte{0xf8, 0x00, 0x01, 0x02},
				})
			}
		}
	}()

	// 客户端只发这一次初始 offer(携带自己要推的音轨)。
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("client %s offer: %v", id, err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("client %s set local offer: %v", id, err)
	}
	c.toServer(sfu.Message{Event: sfu.EventOffer, Data: mustJSON(offer.SDP)})
	return c
}

// TestRoom_TwoWayVoice:两人各推音轨、订阅对方,断言双向都收到 RTP——验证服务端在
// 第二人加入后对第一人的 PeerConnection 发起重协商(否则第一人拿不到第二人的轨道)。
func TestRoom_TwoWayVoice(t *testing.T) {
	api, err := webrtc.NewAPI()
	if err != nil {
		t.Fatalf("new api: %v", err)
	}
	room := sfu.NewRoom(api, sfu.WithName("test"))

	stop := make(chan struct{})
	defer close(stop)

	alice := addClient(t, room, api, "alice", stop)
	// 稍等 alice 完成初始握手,再让 bob 加入,贴近真实的先后进房。
	time.Sleep(500 * time.Millisecond)
	bob := addClient(t, room, api, "bob", stop)

	for name, c := range map[string]*clientPeer{"alice": alice, "bob": bob} {
		select {
		case <-c.gotAudio:
		case <-time.After(30 * time.Second):
			t.Fatalf("%s 30s 内未收到对方音频 RTP", name)
		}
	}

	if n := room.Count(); n != 2 {
		t.Fatalf("room count = %d, want 2", n)
	}

	// bob 离开后房间人数回落。
	_ = bob.pc.Close() // 触发服务端 OnConnectionStateChange → Participant.Close
	deadline := time.After(10 * time.Second)
	for room.Count() != 1 {
		select {
		case <-deadline:
			t.Fatalf("bob 离开后 room count = %d, want 1", room.Count())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
