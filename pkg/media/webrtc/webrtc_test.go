package webrtc_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	pion "github.com/pion/webrtc/v4"

	"github.com/rushteam/beauty/pkg/media/webrtc"
)

// postSDP 发一个 WHIP/WHEP offer,返回 answer SDP 与 Location(资源地址)。
func postSDP(t *testing.T, url, offer string) (answer, location string) {
	t.Helper()
	resp, err := http.Post(url, "application/sdp", strings.NewReader(offer))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("post %s: status %d, body %q", url, resp.StatusCode, body)
	}
	return string(body), resp.Header.Get("Location")
}

// offerAndGather 建一个 offerer PC、生成 offer 并等 ICE 收集完成,返回 offer SDP。
func offerAndGather(t *testing.T, pc *pion.PeerConnection) string {
	t.Helper()
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gc := pion.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local offer: %v", err)
	}
	<-gc
	return pc.LocalDescription().SDP
}

// TestEndToEnd_Relay 端到端验证最小 SFU:publisher --WHIP--> 服务端 Pipe 转发到共享
// 本地轨道 --WHEP--> viewer,断言 viewer 真的收到了 RTP 包;再验证 DELETE 拆除资源。
func TestEndToEnd_Relay(t *testing.T) {
	api, err := webrtc.NewAPI()
	if err != nil {
		t.Fatalf("new api: %v", err)
	}

	var (
		mu     sync.Mutex
		shared *pion.TrackLocalStaticRTP
	)
	trackReady := make(chan struct{})
	var readyOnce sync.Once

	whip := webrtc.NewWHIP(api, func(_ string, pc *pion.PeerConnection) error {
		pc.OnTrack(func(remote *pion.TrackRemote, _ *pion.RTPReceiver) {
			local, err := webrtc.NewLocalTrackFor(remote, "video", "sfu")
			if err != nil {
				return
			}
			mu.Lock()
			shared = local
			mu.Unlock()
			readyOnce.Do(func() { close(trackReady) })
			_ = webrtc.Pipe(context.Background(), remote, local)
		})
		return nil
	}, webrtc.WithBasePath("/whip"))

	whep := webrtc.NewWHEP(api, func(_ string, pc *pion.PeerConnection) error {
		select {
		case <-trackReady:
		case <-time.After(10 * time.Second):
			t.Error("publisher track 未就绪")
		}
		mu.Lock()
		local := shared
		mu.Unlock()
		_, err := pc.AddTrack(local)
		return err
	}, webrtc.WithBasePath("/whep"))

	mux := http.NewServeMux()
	mux.Handle("/whip/", whip)
	mux.Handle("/whep/", whep)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ---- publisher:offerer + sendonly video track,持续写 RTP ----
	pubPC, err := api.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatalf("pub pc: %v", err)
	}
	defer pubPC.Close()
	pubTrack, err := pion.NewTrackLocalStaticRTP(
		pion.RTPCodecCapability{MimeType: pion.MimeTypeVP8, ClockRate: 90000},
		"video", "publisher",
	)
	if err != nil {
		t.Fatalf("pub track: %v", err)
	}
	if _, err := pubPC.AddTrack(pubTrack); err != nil {
		t.Fatalf("pub add track: %v", err)
	}
	pubOffer := offerAndGather(t, pubPC)
	pubAnswer, whipLoc := postSDP(t, srv.URL+"/whip/room1", pubOffer)
	if err := pubPC.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: pubAnswer}); err != nil {
		t.Fatalf("pub set answer: %v", err)
	}

	// 持续推 RTP,直到测试结束。
	stopPub := make(chan struct{})
	defer close(stopPub)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		var seq uint16
		var ts uint32
		for {
			select {
			case <-stopPub:
				return
			case <-ticker.C:
				seq++
				ts += 3000
				_ = pubTrack.WriteRTP(&rtp.Packet{
					Header:  rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, SSRC: 0x1234},
					Payload: []byte{0x10, 0x00, 0x00, 0x01, 0x02, 0x03},
				})
			}
		}
	}()

	// ---- viewer:offerer + recvonly video,收到 RTP 即算成功 ----
	viewPC, err := api.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatalf("view pc: %v", err)
	}
	defer viewPC.Close()
	if _, err := viewPC.AddTransceiverFromKind(pion.RTPCodecTypeVideo,
		pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("view transceiver: %v", err)
	}
	gotRTP := make(chan struct{})
	var gotOnce sync.Once
	viewPC.OnTrack(func(remote *pion.TrackRemote, _ *pion.RTPReceiver) {
		for {
			if _, _, err := remote.ReadRTP(); err != nil {
				return
			}
			gotOnce.Do(func() { close(gotRTP) })
		}
	})
	viewOffer := offerAndGather(t, viewPC)
	viewAnswer, _ := postSDP(t, srv.URL+"/whep/room1", viewOffer)
	if err := viewPC.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: viewAnswer}); err != nil {
		t.Fatalf("view set answer: %v", err)
	}

	select {
	case <-gotRTP:
		// 收到转发过来的 RTP —— 端到端打通。
	case <-time.After(20 * time.Second):
		t.Fatal("viewer 20s 内未收到 RTP")
	}

	// ---- 验证 DELETE 拆除 WHIP 资源 ----
	if whipLoc == "" {
		t.Fatal("WHIP 响应缺少 Location")
	}
	req, err := http.NewRequest(http.MethodDelete, srv.URL+whipLoc, nil)
	if err != nil {
		t.Fatalf("delete request (loc=%q): %v", whipLoc, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", resp.StatusCode)
	}
}

// TestEndpoint_Reject:negotiate 返回 error 时回 403,且不建立会话。
func TestEndpoint_Reject(t *testing.T) {
	api, err := webrtc.NewAPI()
	if err != nil {
		t.Fatalf("new api: %v", err)
	}
	ep := webrtc.NewWHIP(api, func(_ string, _ *pion.PeerConnection) error {
		return io.EOF // 任意拒绝原因
	})
	srv := httptest.NewServer(http.StripPrefix("/whip/", ep))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/whip/x", "application/sdp", strings.NewReader("v=0\r\n"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if n := ep.Count(); n != 0 {
		t.Fatalf("session count = %d, want 0", n)
	}
}

// TestEndpoint_MethodNotAllowed:非 POST/DELETE/OPTIONS 回 405。
func TestEndpoint_MethodNotAllowed(t *testing.T) {
	api, err := webrtc.NewAPI()
	if err != nil {
		t.Fatalf("new api: %v", err)
	}
	ep := webrtc.NewWHEP(api, func(_ string, _ *pion.PeerConnection) error { return nil })
	srv := httptest.NewServer(ep)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}
