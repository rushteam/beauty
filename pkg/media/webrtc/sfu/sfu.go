// Package sfu 在 pkg/media/webrtc 之上提供一个「会议室」SFU 原语:多人实时音视频,
// 每个参会者推自己的轨道、订阅其他所有人的轨道,服务端做选择性转发(Selective
// Forwarding Unit,不混流不转码)。
//
// 和 WHIP/WHEP(pkg/media/webrtc)的区别——那两个是「单路进/单路出」,天生不处理
// 动态成员;本包面向 N↔N 且成员随时进出,因此需要一条持久信令通道 + 服务端重协商:
//   - 信令通道:传输无关。Room 只通过 send 回调把信令消息推给某个参会者,由调用方
//     用 WebSocket(如 pkg/ws)/其它承载(见 examples/webrtc-voice-room)。
//   - 重协商模型(无 glare):客户端只在加入时发一次 offer(携带自己要推的轨道);此后
//     成员进出导致的轨道增减,一律由服务端作为 offerer 推新 offer、客户端 answer。
//     客户端永不二次 offer,从根上避免双方同时 offer 的碰撞(glare)。
//
// 边界(机制而非策略):本包只管房间成员、轨道扇出转发(RTP 原样转发)、重协商与
// trickle ICE。谁能进哪个房间(鉴权)、只开音频还是带视频、混流/主讲人检测/大小流、
// 录制、STUN/TURN 选路、房间生命周期,全是 policy,留给上层。
package sfu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pion "github.com/pion/webrtc/v4"

	"github.com/rushteam/beauty/pkg/media/webrtc"
)

// 信令事件名(与浏览器端约定一致)。
const (
	EventOffer     = "offer"
	EventAnswer    = "answer"
	EventCandidate = "candidate"
)

// Message 是一条信令消息。Data 承载「自然的 JSON 值」:
//   - offer/answer:SDP 字符串(JSON 里就是一个字符串);
//   - candidate:一个 ICECandidateInit 对象。
//
// 两端对称,不做二次编码。
type Message struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func msg(event string, v any) Message {
	b, _ := json.Marshal(v)
	return Message{Event: event, Data: b}
}

const (
	resyncMaxAttempts = 20
	resyncRetryDelay  = 200 * time.Millisecond
)

// Room 是一个 SFU 会议室。零值不可用,用 NewRoom 构造。
type Room struct {
	api    *pion.API
	config pion.Configuration
	name   string

	mu     sync.Mutex
	peers  map[string]*Participant
	tracks map[string]*trackEntry // key: 本地转发轨道 ID
}

type trackEntry struct {
	owner string // 发布该轨道的参会者 id
	local *pion.TrackLocalStaticRTP
}

// Option 配置 Room。
type Option func(*Room)

// WithICEServers 配置 STUN/TURN(跨 NAT 必需;仅局域网/本机可省)。
func WithICEServers(servers ...pion.ICEServer) Option {
	return func(r *Room) { r.config.ICEServers = append(r.config.ICEServers, servers...) }
}

// WithConfiguration 直接覆盖 PeerConnection 配置。
func WithConfiguration(c pion.Configuration) Option {
	return func(r *Room) { r.config = c }
}

// WithName 设置房间名(日志标识用)。
func WithName(name string) Option {
	return func(r *Room) {
		if name != "" {
			r.name = name
		}
	}
}

// NewRoom 创建一个 SFU 会议室。api 由 webrtc.NewAPI() 得到。
func NewRoom(api *pion.API, opts ...Option) *Room {
	r := &Room{
		api:    api,
		name:   "room",
		peers:  make(map[string]*Participant),
		tracks: make(map[string]*trackEntry),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Count 返回当前参会人数。
func (r *Room) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers)
}

// Participant 是一个已加入房间的参会者(对应一条 PeerConnection + 一条信令通道)。
type Participant struct {
	id   string
	room *Room
	pc   *pion.PeerConnection
	send func(Message) error

	negoMu sync.Mutex // 串行化本 pc 上的协商(加/删轨 + 发 offer)

	mu        sync.Mutex
	pending   []pion.ICECandidateInit // 远端描述就绪前缓冲的候选
	closeOnce sync.Once
}

// ID 返回参会者 id。
func (p *Participant) ID() string { return p.id }

// Join 让一个参会者加入房间:新建 PeerConnection、接好 trickle ICE 与收流转发,登记进
// 房间。send 用于把服务端产生的信令消息推给该客户端(须并发安全或由调用方串行化)。
// 之后调用方从信令通道读到的每条消息交给 Participant.HandleSignal。
func (r *Room) Join(id string, send func(Message) error) (*Participant, error) {
	pc, err := r.api.NewPeerConnection(r.config)
	if err != nil {
		return nil, fmt.Errorf("sfu: new peerconnection: %w", err)
	}
	p := &Participant{id: id, room: r, pc: pc, send: send}

	// 服务端 → 客户端 trickle ICE。
	pc.OnICECandidate(func(c *pion.ICECandidate) {
		if c == nil {
			return
		}
		if err := p.send(msg(EventCandidate, c.ToJSON())); err != nil {
			slog.Debug("sfu: send candidate failed", "room", r.name, "peer", id, "err", err)
		}
	})

	// 参会者推上来的每路轨道 → 建一路本地转发轨道 → Pipe 扇出给其他人。
	pc.OnTrack(func(remote *pion.TrackRemote, _ *pion.RTPReceiver) {
		trackID := fmt.Sprintf("%s-%s-%s", id, remote.Kind(), remote.ID())
		local, err := webrtc.NewLocalTrackFor(remote, trackID, id)
		if err != nil {
			slog.Debug("sfu: new local track failed", "room", r.name, "peer", id, "err", err)
			return
		}
		r.addTrack(id, trackID, local)
		_ = webrtc.Pipe(context.Background(), remote, local) // 转发到轨道结束
		r.removeTrack(trackID)
	})

	pc.OnConnectionStateChange(func(s pion.PeerConnectionState) {
		switch s {
		case pion.PeerConnectionStateFailed, pion.PeerConnectionStateClosed:
			_ = p.Close()
		}
	})

	r.mu.Lock()
	r.peers[id] = p
	r.mu.Unlock()
	return p, nil
}

// HandleSignal 处理一条来自客户端的信令消息(offer/answer/candidate)。
func (p *Participant) HandleSignal(m Message) error {
	switch m.Event {
	case EventOffer:
		var sdp string
		if err := json.Unmarshal(m.Data, &sdp); err != nil {
			return fmt.Errorf("sfu: bad offer: %w", err)
		}
		p.negoMu.Lock()
		defer p.negoMu.Unlock()
		if err := p.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeOffer, SDP: sdp}); err != nil {
			return fmt.Errorf("sfu: set remote offer: %w", err)
		}
		p.flushCandidates()
		answer, err := p.pc.CreateAnswer(nil)
		if err != nil {
			return fmt.Errorf("sfu: create answer: %w", err)
		}
		if err := p.pc.SetLocalDescription(answer); err != nil {
			return fmt.Errorf("sfu: set local answer: %w", err)
		}
		if err := p.send(msg(EventAnswer, answer.SDP)); err != nil {
			return err
		}
		// 初始握手完成后,把房间现有轨道同步给这个新人(并把它的轨道推给别人由 OnTrack 触发)。
		go p.room.resync(0)
		return nil

	case EventAnswer:
		var sdp string
		if err := json.Unmarshal(m.Data, &sdp); err != nil {
			return fmt.Errorf("sfu: bad answer: %w", err)
		}
		p.negoMu.Lock()
		defer p.negoMu.Unlock()
		if err := p.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: sdp}); err != nil {
			return fmt.Errorf("sfu: set remote answer: %w", err)
		}
		p.flushCandidates()
		// 本次协商落定,冲一遍可能被推迟的同步。
		go p.room.resync(0)
		return nil

	case EventCandidate:
		var c pion.ICECandidateInit
		if err := json.Unmarshal(m.Data, &c); err != nil {
			return fmt.Errorf("sfu: bad candidate: %w", err)
		}
		p.mu.Lock()
		if p.pc.RemoteDescription() == nil {
			p.pending = append(p.pending, c) // 远端描述未就绪,先缓冲
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()
		return p.pc.AddICECandidate(c)

	default:
		return fmt.Errorf("sfu: unknown event %q", m.Event)
	}
}

// flushCandidates 在设置远端描述后补加缓冲的候选。调用者须持有 p.negoMu 或在握手路径上。
func (p *Participant) flushCandidates() {
	p.mu.Lock()
	pend := p.pending
	p.pending = nil
	p.mu.Unlock()
	for _, c := range pend {
		if err := p.pc.AddICECandidate(c); err != nil {
			slog.Debug("sfu: add buffered candidate failed", "peer", p.id, "err", err)
		}
	}
}

// Close 让参会者离开:注销、关闭 PeerConnection、移除其发布的轨道并触发其他人重协商。幂等。
func (p *Participant) Close() error {
	p.closeOnce.Do(func() {
		r := p.room
		r.mu.Lock()
		delete(r.peers, p.id)
		for id, te := range r.tracks {
			if te.owner == p.id {
				delete(r.tracks, id)
			}
		}
		r.mu.Unlock()
		_ = p.pc.Close()
		go r.resync(0) // 其他人移除该参会者的轨道并重协商
	})
	return nil
}

func (r *Room) addTrack(owner, id string, local *pion.TrackLocalStaticRTP) {
	r.mu.Lock()
	r.tracks[id] = &trackEntry{owner: owner, local: local}
	r.mu.Unlock()
	r.resync(0)
}

func (r *Room) removeTrack(id string) {
	r.mu.Lock()
	delete(r.tracks, id)
	r.mu.Unlock()
	r.resync(0)
}

// resync 让每个参会者的 PeerConnection 与房间当前轨道集合对齐(补齐别人的轨道、移除已走的),
// 有变化且信令空闲时由服务端发起重协商。若某些 peer 正处于协商中(信令非 stable),推迟后重试。
func (r *Room) resync(attempt int) {
	r.mu.Lock()
	peers := make([]*Participant, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}
	r.mu.Unlock()

	deferred := false
	for _, p := range peers {
		if r.syncPeer(p) {
			deferred = true
		}
	}
	if deferred && attempt < resyncMaxAttempts {
		time.AfterFunc(resyncRetryDelay, func() { r.resync(attempt + 1) })
	}
}

// syncPeer 对齐单个参会者的发送轨道;返回是否因协商未落定而推迟。
func (r *Room) syncPeer(p *Participant) (deferredRetry bool) {
	p.negoMu.Lock()
	defer p.negoMu.Unlock()

	// 期望:房间里所有「非本人」的轨道。
	r.mu.Lock()
	desired := make(map[string]*pion.TrackLocalStaticRTP, len(r.tracks))
	for id, te := range r.tracks {
		if te.owner != p.id {
			desired[id] = te.local
		}
	}
	r.mu.Unlock()

	// 现状:pc 上已挂的发送轨道。
	existing := make(map[string]*pion.RTPSender)
	for _, s := range p.pc.GetSenders() {
		if t := s.Track(); t != nil {
			existing[t.ID()] = s
		}
	}

	changed := false
	for id, s := range existing { // 移除已离场的
		if _, ok := desired[id]; !ok {
			if err := p.pc.RemoveTrack(s); err == nil {
				changed = true
			}
		}
	}
	for id, t := range desired { // 补齐新加入的
		if _, ok := existing[id]; !ok {
			if _, err := p.pc.AddTrack(t); err == nil {
				changed = true
			}
		}
	}
	if !changed {
		return false
	}
	// 上一轮协商还没落定,推迟——answer 到达时会再次触发 resync。
	if p.pc.SignalingState() != pion.SignalingStateStable {
		return true
	}
	if err := p.renegotiate(); err != nil {
		slog.Debug("sfu: renegotiate failed", "room", r.name, "peer", p.id, "err", err)
		return true
	}
	return false
}

// renegotiate 由服务端作为 offerer 发起一次重协商。调用者须持有 p.negoMu。
func (p *Participant) renegotiate() error {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return err
	}
	return p.send(msg(EventOffer, offer.SDP))
}
