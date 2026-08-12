// Package webrtctransport 提供基于 WebRTC DataChannel 的 p2p.Transport 实现。
//
// 适合场景:
//   - 浏览器与 Go 服务之间的 P2P 通信(WebRTC 是唯一的浏览器端 P2P 方案)
//   - Go 进程之间需要穿越复杂 NAT(WebRTC ICE 框架自动 STUN/TURN 穿透)
//   - 需要与前端 JS 客户端(p2p-client.js)对接的场景
//   - 游戏客户端(Web/Electron)与匹配服务器直连
//
// 特点:
//   - 可靠通道:有序 DataChannel(ordered=true, maxRetransmits=nil)
//   - 不可靠通道:无序 DataChannel(ordered=false, maxRetransmits=0)
//   - 内建 ICE 框架——自动 NAT 穿透(STUN + TURN fallback)
//   - 与浏览器 RTCPeerConnection 完全兼容
//   - 需要信令服务配合(pkg/p2p/signaling)交换 SDP/ICE
//
// 与其他 Transport 的区别:
//   - TCP: 最简单但不穿 NAT
//   - QUIC: 高性能双通道但需要公网可达(或自行打洞)
//   - WebRTC: NAT 穿透能力最强 + 唯一能与浏览器对接
//
// 依赖:pion/webrtc v4(Go 实现的 WebRTC 协议栈)。
package webrtctransport

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
	"github.com/rushteam/beauty/pkg/p2p"
)

const (
	reliableLabel   = "reliable"
	unreliableLabel = "unreliable"
)

// SignalMessage 是需要通过信令通道交换的消息。
// 上层(如 pkg/p2p/signaling 客户端)负责在两个 peer 之间传递这些消息。
type SignalMessage struct {
	Type      string `json:"type"`                // "offer", "answer", "candidate"
	SDP       string `json:"sdp,omitempty"`       // offer/answer 的 SDP
	Candidate string `json:"candidate,omitempty"` // ICE candidate JSON
}

// SignalFunc 用于发送信令消息给目标 peer。由上层注入。
type SignalFunc func(ctx context.Context, targetPeer p2p.PeerID, msg SignalMessage) error

// Transport 基于 WebRTC DataChannel 的 P2P 传输。
// 与 TCP/QUIC 不同,WebRTC 不直接"监听",而是通过信令服务驱动连接建立。
type Transport struct {
	localID    p2p.PeerID
	signalFunc SignalFunc
	iceServers []webrtc.ICEServer
	pending    map[p2p.PeerID]*pendingConn
	accept     chan p2p.PeerConn
	mu         sync.Mutex
	closed     chan struct{}
	once       sync.Once
}

type pendingConn struct {
	pc       *webrtc.PeerConnection
	done     chan p2p.PeerConn
	errCh    chan error
	reliable *webrtc.DataChannel
}

// Option 配置 Transport。
type Option func(*Transport)

// WithICEServers 设置 ICE 服务器(STUN/TURN)。
func WithICEServers(servers []webrtc.ICEServer) Option {
	return func(t *Transport) { t.iceServers = servers }
}

// New 创建 WebRTC Transport。signalFunc 是信令发送函数,由上层(信令客户端)提供。
func New(localID p2p.PeerID, signalFunc SignalFunc, opts ...Option) *Transport {
	t := &Transport{
		localID:    localID,
		signalFunc: signalFunc,
		iceServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
		pending:    make(map[p2p.PeerID]*pendingConn),
		accept:     make(chan p2p.PeerConn, 16),
		closed:     make(chan struct{}),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Addr 返回空串——WebRTC 不基于固定地址监听。
func (t *Transport) Addr() string { return "" }

// Connect 主动向目标 peer 发起连接(创建 offer)。
// 调用后需要通过信令将 offer 和 ICE candidate 发给对端。
func (t *Transport) Connect(ctx context.Context, peerID p2p.PeerID) (p2p.PeerConn, error) {
	pc, err := t.createPeerConnection()
	if err != nil {
		return nil, err
	}

	pending := &pendingConn{
		pc:    pc,
		done:  make(chan p2p.PeerConn, 1),
		errCh: make(chan error, 1),
	}

	// 创建 DataChannels(发起方)
	reliable, err := pc.CreateDataChannel(reliableLabel, &webrtc.DataChannelInit{
		Ordered: boolPtr(true),
	})
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("webrtctransport: create reliable channel: %w", err)
	}
	pending.reliable = reliable

	unreliable, err := pc.CreateDataChannel(unreliableLabel, &webrtc.DataChannelInit{
		Ordered:        boolPtr(false),
		MaxRetransmits: uint16Ptr(0),
	})
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("webrtctransport: create unreliable channel: %w", err)
	}

	t.mu.Lock()
	t.pending[peerID] = pending
	t.mu.Unlock()

	// ICE candidate 回调
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		candidateJSON, _ := json.Marshal(c.ToJSON())
		_ = t.signalFunc(ctx, peerID, SignalMessage{
			Type:      "candidate",
			Candidate: string(candidateJSON),
		})
	})

	// 等待两个 channel 都就绪
	conn := newWebRTCConn(peerID, pc, reliable, unreliable)

	// 创建 offer
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("webrtctransport: create offer: %w", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, fmt.Errorf("webrtctransport: set local desc: %w", err)
	}

	// 发送 offer
	if err := t.signalFunc(ctx, peerID, SignalMessage{Type: "offer", SDP: offer.SDP}); err != nil {
		pc.Close()
		return nil, fmt.Errorf("webrtctransport: send offer: %w", err)
	}

	pending.done <- conn

	// 等待连接完成
	select {
	case <-conn.ready:
		return conn, nil
	case <-ctx.Done():
		pc.Close()
		return nil, ctx.Err()
	}
}

// Accept 阻塞等待入站连接(对端发起 offer 后,通过 HandleSignal 处理)。
func (t *Transport) Accept(ctx context.Context) (p2p.PeerConn, error) {
	select {
	case pc := <-t.accept:
		return pc, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closed:
		return nil, fmt.Errorf("webrtctransport: closed")
	}
}

// HandleSignal 处理从信令通道收到的消息。上层信令客户端收到 signal 事件后调用。
func (t *Transport) HandleSignal(ctx context.Context, fromPeer p2p.PeerID, msg SignalMessage) error {
	switch msg.Type {
	case "offer":
		return t.handleOffer(ctx, fromPeer, msg)
	case "answer":
		return t.handleAnswer(fromPeer, msg)
	case "candidate":
		return t.handleCandidate(fromPeer, msg)
	default:
		return fmt.Errorf("webrtctransport: unknown signal type %q", msg.Type)
	}
}

func (t *Transport) handleOffer(ctx context.Context, fromPeer p2p.PeerID, msg SignalMessage) error {
	pc, err := t.createPeerConnection()
	if err != nil {
		return err
	}

	pending := &pendingConn{
		pc:    pc,
		done:  make(chan p2p.PeerConn, 1),
		errCh: make(chan error, 1),
	}

	t.mu.Lock()
	t.pending[fromPeer] = pending
	t.mu.Unlock()

	var conn *webrtcConn

	// 接收方:等待对端创建的 DataChannel
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		t.mu.Lock()
		p := t.pending[fromPeer]
		t.mu.Unlock()
		if p == nil {
			return
		}

		switch dc.Label() {
		case reliableLabel:
			if conn == nil {
				conn = newWebRTCConn(fromPeer, pc, dc, nil)
				p.done <- conn
			} else {
				conn.setReliable(dc)
			}
		case unreliableLabel:
			if conn == nil {
				conn = newWebRTCConn(fromPeer, pc, nil, dc)
				p.done <- conn
			} else {
				conn.setUnreliable(dc)
			}
		}
	})

	// ICE candidate 回调
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		candidateJSON, _ := json.Marshal(c.ToJSON())
		_ = t.signalFunc(ctx, fromPeer, SignalMessage{
			Type:      "candidate",
			Candidate: string(candidateJSON),
		})
	})

	// 设置远端 SDP
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  msg.SDP,
	}); err != nil {
		pc.Close()
		return fmt.Errorf("webrtctransport: set remote desc: %w", err)
	}

	// 创建 answer
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return fmt.Errorf("webrtctransport: create answer: %w", err)
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		return fmt.Errorf("webrtctransport: set local desc: %w", err)
	}

	// 发送 answer
	if err := t.signalFunc(ctx, fromPeer, SignalMessage{Type: "answer", SDP: answer.SDP}); err != nil {
		pc.Close()
		return err
	}

	// 等待 DataChannel 就绪后推入 accept
	go func() {
		select {
		case c := <-pending.done:
			wc := c.(*webrtcConn)
			<-wc.ready
			t.accept <- wc
		case <-t.closed:
			pc.Close()
		}
	}()

	return nil
}

func (t *Transport) handleAnswer(fromPeer p2p.PeerID, msg SignalMessage) error {
	t.mu.Lock()
	pending, ok := t.pending[fromPeer]
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("webrtctransport: no pending connection for %q", fromPeer)
	}

	return pending.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  msg.SDP,
	})
}

func (t *Transport) handleCandidate(fromPeer p2p.PeerID, msg SignalMessage) error {
	t.mu.Lock()
	pending, ok := t.pending[fromPeer]
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("webrtctransport: no pending connection for %q", fromPeer)
	}

	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal([]byte(msg.Candidate), &candidate); err != nil {
		return fmt.Errorf("webrtctransport: unmarshal candidate: %w", err)
	}

	return pending.pc.AddICECandidate(candidate)
}

func (t *Transport) createPeerConnection() (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{ICEServers: t.iceServers}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("webrtctransport: new peer connection: %w", err)
	}
	return pc, nil
}

// Close 关闭 Transport。
func (t *Transport) Close() error {
	t.once.Do(func() {
		close(t.closed)
		t.mu.Lock()
		for _, p := range t.pending {
			p.pc.Close()
		}
		t.pending = nil
		t.mu.Unlock()
	})
	return nil
}

// ─── webrtcConn 实现 p2p.PeerConn ───

type webrtcConn struct {
	peerID     p2p.PeerID
	pc         *webrtc.PeerConnection
	reliable   *webrtc.DataChannel
	unreliable *webrtc.DataChannel
	recv       chan p2p.Message
	ctx        context.Context
	cancel     context.CancelFunc
	ready      chan struct{}
	closeOnce  sync.Once
	mu         sync.Mutex
	chReady    int // 已就绪的 channel 数
}

func newWebRTCConn(peerID p2p.PeerID, pc *webrtc.PeerConnection, reliable, unreliable *webrtc.DataChannel) *webrtcConn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &webrtcConn{
		peerID: peerID,
		pc:     pc,
		recv:   make(chan p2p.Message, 64),
		ctx:    ctx,
		cancel: cancel,
		ready:  make(chan struct{}),
	}

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			c.Close()
		}
	})

	if reliable != nil {
		c.reliable = reliable
		c.setupChannel(reliable, true)
	}
	if unreliable != nil {
		c.unreliable = unreliable
		c.setupChannel(unreliable, false)
	}

	return c
}

func (c *webrtcConn) setReliable(dc *webrtc.DataChannel) {
	c.mu.Lock()
	c.reliable = dc
	c.mu.Unlock()
	c.setupChannel(dc, true)
}

func (c *webrtcConn) setUnreliable(dc *webrtc.DataChannel) {
	c.mu.Lock()
	c.unreliable = dc
	c.mu.Unlock()
	c.setupChannel(dc, false)
}

func (c *webrtcConn) setupChannel(dc *webrtc.DataChannel, isReliable bool) {
	dc.OnOpen(func() {
		c.mu.Lock()
		c.chReady++
		allReady := c.chReady >= 2
		c.mu.Unlock()
		if allReady {
			select {
			case <-c.ready:
			default:
				close(c.ready)
			}
		}
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		select {
		case c.recv <- p2p.Message{From: c.peerID, Data: msg.Data, Reliable: isReliable}:
		case <-c.ctx.Done():
		}
	})

	dc.OnClose(func() {
		c.Close()
	})
}

func (c *webrtcConn) ID() p2p.PeerID           { return c.peerID }
func (c *webrtcConn) Recv() <-chan p2p.Message { return c.recv }
func (c *webrtcConn) Context() context.Context { return c.ctx }

func (c *webrtcConn) SendReliable(data []byte) error {
	c.mu.Lock()
	ch := c.reliable
	c.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("webrtctransport: reliable channel not ready")
	}
	return ch.Send(data)
}

func (c *webrtcConn) SendUnreliable(data []byte) error {
	c.mu.Lock()
	ch := c.unreliable
	c.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("webrtctransport: unreliable channel not ready")
	}
	return ch.Send(data)
}

func (c *webrtcConn) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		c.pc.Close()
	})
	return nil
}

// ─── helpers ───

func boolPtr(v bool) *bool       { return &v }
func uint16Ptr(v uint16) *uint16 { return &v }
