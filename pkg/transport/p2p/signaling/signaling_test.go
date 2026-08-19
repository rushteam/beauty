package signaling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/rushteam/beauty/pkg/transport/p2p/topology"
)

func TestServer_JoinAndRelay(t *testing.T) {
	srv := NewServer(WithWSOptions())
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	connA, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.CloseNow()

	joinA := Envelope{Event: EventJoin}
	joinA.Data, _ = json.Marshal(JoinPayload{Room: "test-room", PeerID: "A"})
	if err := wsjson.Write(ctx, connA, joinA); err != nil {
		t.Fatalf("A join: %v", err)
	}

	var envA Envelope
	if err := wsjson.Read(ctx, connA, &envA); err != nil {
		t.Fatalf("A read assign_id: %v", err)
	}
	if envA.Event != EventAssignID {
		t.Fatalf("expected assign_id, got %s", envA.Event)
	}
	var assignA AssignIDPayload
	json.Unmarshal(envA.Data, &assignA)
	if assignA.PeerID != "A" {
		t.Fatalf("expected peer_id=A, got %s", assignA.PeerID)
	}
	// 验证通道配置
	if len(assignA.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(assignA.Channels))
	}

	connB, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()

	joinB := Envelope{Event: EventJoin}
	joinB.Data, _ = json.Marshal(JoinPayload{Room: "test-room", PeerID: "B"})
	if err := wsjson.Write(ctx, connB, joinB); err != nil {
		t.Fatalf("B join: %v", err)
	}

	var envB Envelope
	if err := wsjson.Read(ctx, connB, &envB); err != nil {
		t.Fatalf("B read assign_id: %v", err)
	}
	if envB.Event != EventAssignID {
		t.Fatalf("expected assign_id, got %s", envB.Event)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	var peerJoinedA, peerJoinedB PeerJoinedPayload
	go func() {
		defer wg.Done()
		var e Envelope
		if err := wsjson.Read(ctx, connA, &e); err != nil {
			t.Errorf("A read peer_joined: %v", err)
			return
		}
		if e.Event != EventPeerJoined {
			t.Errorf("A expected peer_joined, got %s", e.Event)
			return
		}
		json.Unmarshal(e.Data, &peerJoinedA)
	}()
	go func() {
		defer wg.Done()
		var e Envelope
		if err := wsjson.Read(ctx, connB, &e); err != nil {
			t.Errorf("B read peer_joined: %v", err)
			return
		}
		if e.Event != EventPeerJoined {
			t.Errorf("B expected peer_joined, got %s", e.Event)
			return
		}
		json.Unmarshal(e.Data, &peerJoinedB)
	}()
	wg.Wait()

	if !peerJoinedA.Initiator {
		t.Error("A should be initiator")
	}
	if peerJoinedA.PeerID != "B" {
		t.Errorf("A peer_joined should reference B, got %s", peerJoinedA.PeerID)
	}
	if peerJoinedB.Initiator {
		t.Error("B should NOT be initiator")
	}

	// A 中转一条信令给 B
	offer := json.RawMessage(`{"type":"offer","sdp":"v=0..."}`)
	relay := Envelope{Event: EventRelay}
	relay.Data, _ = json.Marshal(RelayPayload{To: "B", Data: offer})
	if err := wsjson.Write(ctx, connA, relay); err != nil {
		t.Fatalf("A relay: %v", err)
	}

	var sigEnv Envelope
	if err := wsjson.Read(ctx, connB, &sigEnv); err != nil {
		t.Fatalf("B read signal: %v", err)
	}
	if sigEnv.Event != EventSignal {
		t.Fatalf("B expected signal, got %s", sigEnv.Event)
	}
	var sig SignalPayload
	json.Unmarshal(sigEnv.Data, &sig)
	if sig.From != "A" {
		t.Errorf("signal from should be A, got %s", sig.From)
	}

	if srv.PeerCount("test-room") != 2 {
		t.Errorf("expected 2 peers in room, got %d", srv.PeerCount("test-room"))
	}
}

func TestServer_PeerLeave(t *testing.T) {
	srv := NewServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	connA, _, _ := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	defer connA.CloseNow()
	joinEnv := Envelope{Event: EventJoin}
	joinEnv.Data, _ = json.Marshal(JoinPayload{Room: "room1", PeerID: "A"})
	wsjson.Write(ctx, connA, joinEnv)
	var e Envelope
	wsjson.Read(ctx, connA, &e)

	connB, _, _ := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	joinEnv.Data, _ = json.Marshal(JoinPayload{Room: "room1", PeerID: "B"})
	wsjson.Write(ctx, connB, joinEnv)
	wsjson.Read(ctx, connB, &e)

	wsjson.Read(ctx, connA, &e)
	wsjson.Read(ctx, connB, &e)

	connB.Close(websocket.StatusNormalClosure, "bye")

	var leftEnv Envelope
	if err := wsjson.Read(ctx, connA, &leftEnv); err != nil {
		t.Fatalf("A read peer_left: %v", err)
	}
	if leftEnv.Event != EventPeerLeft {
		t.Fatalf("expected peer_left, got %s", leftEnv.Event)
	}
	var left PeerLeftPayload
	json.Unmarshal(leftEnv.Data, &left)
	if left.PeerID != "B" {
		t.Errorf("expected peer B left, got %s", left.PeerID)
	}

	time.Sleep(100 * time.Millisecond)
	if srv.PeerCount("room1") != 1 {
		t.Errorf("expected 1 peer remaining, got %d", srv.PeerCount("room1"))
	}
}

func TestServer_KeepAlive(t *testing.T) {
	srv := NewServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, _ := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	defer conn.CloseNow()

	joinEnv := Envelope{Event: EventJoin}
	joinEnv.Data, _ = json.Marshal(JoinPayload{Room: "keepalive-room", PeerID: "KA"})
	wsjson.Write(ctx, conn, joinEnv)

	var e Envelope
	wsjson.Read(ctx, conn, &e) // assign_id

	// 发送 keep_alive
	ping := Envelope{Event: EventPing}
	if err := wsjson.Write(ctx, conn, ping); err != nil {
		t.Fatalf("send keep_alive: %v", err)
	}

	// 应收到 keep_alive 响应
	var resp Envelope
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read keep_alive response: %v", err)
	}
	if resp.Event != EventKeepAlive {
		t.Fatalf("expected keep_alive response, got %s", resp.Event)
	}
}

func TestServer_ConnectionAuth(t *testing.T) {
	srv := NewServer(
		WithConnectionAuth(func(r *http.Request) bool {
			return r.URL.Query().Get("token") == "secret"
		}),
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 无 token:应被拒绝
	_, resp, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	if err == nil {
		t.Fatal("expected connection to be rejected")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}

	// 有 token:应成功
	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws?token=secret", nil)
	if err != nil {
		t.Fatalf("expected connection to succeed: %v", err)
	}
	conn.CloseNow()
}

func TestServer_LifecycleCallbacks(t *testing.T) {
	var connected, disconnected atomic.Int32
	var roomCreated, roomEmpty atomic.Int32

	srv := NewServer(WithCallbacks(Callbacks{
		OnPeerConnected:    func(_ string, _ string) { connected.Add(1) },
		OnPeerDisconnected: func(_ string, _ string) { disconnected.Add(1) },
		OnRoomCreated:      func(_ string) { roomCreated.Add(1) },
		OnRoomEmpty:        func(_ string) { roomEmpty.Add(1) },
	}))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, _ := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	joinEnv := Envelope{Event: EventJoin}
	joinEnv.Data, _ = json.Marshal(JoinPayload{Room: "cb-room", PeerID: "X"})
	wsjson.Write(ctx, conn, joinEnv)
	var e Envelope
	wsjson.Read(ctx, conn, &e) // assign_id

	time.Sleep(50 * time.Millisecond)
	if connected.Load() != 1 {
		t.Errorf("expected OnPeerConnected called once, got %d", connected.Load())
	}
	if roomCreated.Load() != 1 {
		t.Errorf("expected OnRoomCreated called once, got %d", roomCreated.Load())
	}

	conn.Close(websocket.StatusNormalClosure, "bye")
	time.Sleep(100 * time.Millisecond)

	if disconnected.Load() != 1 {
		t.Errorf("expected OnPeerDisconnected called once, got %d", disconnected.Load())
	}
	if roomEmpty.Load() != 1 {
		t.Errorf("expected OnRoomEmpty called once, got %d", roomEmpty.Load())
	}
}

func TestServer_WaitForN(t *testing.T) {
	srv := NewServer(WithTopology(topology.NewWaitForN(3)))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conns := make([]*websocket.Conn, 3)
	for i := range conns {
		c, _, _ := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
		conns[i] = c
		defer c.CloseNow()
		joinEnv := Envelope{Event: EventJoin}
		joinEnv.Data, _ = json.Marshal(JoinPayload{Room: "wait3", PeerID: string(rune('A' + i))})
		wsjson.Write(ctx, c, joinEnv)
		var e Envelope
		wsjson.Read(ctx, c, &e) // assign_id
	}

	// 凑够 3 人后,每个人应收到 peer_joined 通知
	// peer A 应收到 2 条 peer_joined(与 B 和 C 建连)
	received := 0
	for i := 0; i < 2; i++ {
		var e Envelope
		if err := wsjson.Read(ctx, conns[0], &e); err != nil {
			t.Fatalf("A read: %v", err)
		}
		if e.Event == EventPeerJoined {
			received++
		}
	}
	if received != 2 {
		t.Errorf("expected A to receive 2 peer_joined, got %d", received)
	}
}

func TestServer_ClientServerTopology(t *testing.T) {
	cs := topology.NewClientServer()
	srv := NewServer(WithTopology(cs))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 第一个加入的成为 host
	connHost, _, _ := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	defer connHost.CloseNow()
	joinEnv := Envelope{Event: EventJoin}
	joinEnv.Data, _ = json.Marshal(JoinPayload{Room: "cs-room", PeerID: "HOST"})
	wsjson.Write(ctx, connHost, joinEnv)
	var e Envelope
	wsjson.Read(ctx, connHost, &e) // assign_id

	if cs.Host() != "HOST" {
		t.Fatalf("expected host=HOST, got %s", cs.Host())
	}

	// client 加入:应与 host 建连
	connClient, _, _ := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	defer connClient.CloseNow()
	joinEnv.Data, _ = json.Marshal(JoinPayload{Room: "cs-room", PeerID: "CLIENT"})
	wsjson.Write(ctx, connClient, joinEnv)
	wsjson.Read(ctx, connClient, &e) // assign_id

	// host 应收到 peer_joined(initiator=true,因为 host 是 initiator)
	var hostEvent Envelope
	if err := wsjson.Read(ctx, connHost, &hostEvent); err != nil {
		t.Fatalf("host read: %v", err)
	}
	if hostEvent.Event != EventPeerJoined {
		t.Fatalf("host expected peer_joined, got %s", hostEvent.Event)
	}
	var pj PeerJoinedPayload
	json.Unmarshal(hostEvent.Data, &pj)
	if !pj.Initiator {
		t.Error("host should be initiator")
	}
	if pj.PeerID != "CLIENT" {
		t.Errorf("expected CLIENT, got %s", pj.PeerID)
	}
}

func TestServer_MultiChannelConfig(t *testing.T) {
	srv := NewServer(WithChannels(
		ReliableChannel("chat"),
		UnreliableChannel("position"),
		SemiReliableChannel("events", 3),
	))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, _ := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws", nil)
	defer conn.CloseNow()

	joinEnv := Envelope{Event: EventJoin}
	joinEnv.Data, _ = json.Marshal(JoinPayload{Room: "multi-ch"})
	wsjson.Write(ctx, conn, joinEnv)

	var e Envelope
	wsjson.Read(ctx, conn, &e)

	var assign AssignIDPayload
	json.Unmarshal(e.Data, &assign)

	if len(assign.Channels) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(assign.Channels))
	}
	if assign.Channels[0].Label != "chat" || !assign.Channels[0].Ordered {
		t.Errorf("channel 0 wrong: %+v", assign.Channels[0])
	}
	if assign.Channels[1].Label != "position" || assign.Channels[1].Ordered {
		t.Errorf("channel 1 wrong: %+v", assign.Channels[1])
	}
	if assign.Channels[2].Label != "events" || assign.Channels[2].MaxRetransmits == nil || *assign.Channels[2].MaxRetransmits != 3 {
		t.Errorf("channel 2 wrong: %+v", assign.Channels[2])
	}
}
