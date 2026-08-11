// Package signaling 提供面向 P2P DataChannel 的信令服务:通过 WebSocket 中继
// WebRTC 协商消息(offer/answer/candidate),让 peers 能自动发现、配对、建立
// 直连的 DataChannel——无需业务代码接触底层 ICE/SDP 细节。
//
// 设计受 matchbox(https://github.com/johanhelsing/matchbox)启发:
//   - 极简协议:JSON 信令消息 + 房间维度的 peer 发现
//   - 拓扑可插拔:通过 pkg/p2p/topology 控制谁与谁建连
//   - KeepAlive 防断:信令层定期心跳,防止反向代理关闭空闲连接
//   - 连接鉴权钩子:WebSocket 升级前即可拒绝非法连接
//   - 生命周期回调:peer 连接/断开时触发服务端逻辑(统计/清理/迁移)
//   - 多通道配置:支持 N 条 DataChannel(可靠/不可靠/有限重传)
//   - ?next=N 凑人:等凑够 N 人再一起触发连接
//
// 边界(机制而非策略):本包只处理信令中继与 peer 发现。STUN/TURN 配置、
// DataChannel 上的应用协议由上层决定。
package signaling

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/rushteam/beauty/pkg/p2p"
	"github.com/rushteam/beauty/pkg/p2p/topology"
	"github.com/rushteam/beauty/pkg/ws"
)

// ─── 信令协议常量 ───

const (
	// 服务端 → 客户端
	EventPeerJoined = "peer_joined" // 新 peer 加入房间
	EventPeerLeft   = "peer_left"   // peer 离开房间
	EventSignal     = "signal"      // 中转的信令消息(offer/answer/candidate)
	EventAssignID   = "assign_id"   // 分配 peer ID
	EventKeepAlive  = "keep_alive"  // 心跳响应

	// 客户端 → 服务端
	EventJoin  = "join"       // 请求加入房间
	EventRelay = "relay"      // 请求中转信令消息给目标 peer
	EventLeave = "leave"      // 请求离开房间
	EventPing  = "keep_alive" // 心跳请求(与响应同名,服务端收到后回复)
)

// ─── 协议消息体 ───

// Envelope 是客户端与信令服务之间传输的消息信封。
type Envelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// JoinPayload 客户端加入房间的请求载荷。
type JoinPayload struct {
	Room   string `json:"room"`
	PeerID string `json:"peer_id,omitempty"` // 可选,不填则服务端分配
}

// RelayPayload 客户端请求中转的信令消息。
type RelayPayload struct {
	To   string          `json:"to"`   // 目标 peer ID
	Data json.RawMessage `json:"data"` // 透传内容(offer/answer/candidate)
}

// PeerJoinedPayload 服务端通知:新 peer 加入。
type PeerJoinedPayload struct {
	PeerID    string `json:"peer_id"`
	Initiator bool   `json:"initiator"` // 收到此消息的 peer 是否应主动发起 offer
}

// PeerLeftPayload 服务端通知:peer 离开。
type PeerLeftPayload struct {
	PeerID string `json:"peer_id"`
}

// SignalPayload 服务端转发的信令消息。
type SignalPayload struct {
	From string          `json:"from"` // 来源 peer ID
	Data json.RawMessage `json:"data"` // 透传内容
}

// AssignIDPayload 服务端分配的 peer ID。
type AssignIDPayload struct {
	PeerID   string          `json:"peer_id"`
	Channels []ChannelConfig `json:"channels,omitempty"` // 通道配置(告知客户端应创建哪些 DataChannel)
}

// ─── 多通道配置 ───

// ChannelConfig 描述一条 DataChannel 的配置,序列化后发给客户端。
type ChannelConfig struct {
	Label          string `json:"label"`                     // 通道标签名
	Ordered        bool   `json:"ordered"`                   // 是否保序
	MaxRetransmits *int   `json:"max_retransmits,omitempty"` // 最大重传次数(nil=无限重传=完全可靠)
}

// ReliableChannel 返回一个可靠有序通道配置。
func ReliableChannel(label string) ChannelConfig {
	return ChannelConfig{Label: label, Ordered: true, MaxRetransmits: nil}
}

// UnreliableChannel 返回一个不可靠无序通道配置(不重传)。
func UnreliableChannel(label string) ChannelConfig {
	zero := 0
	return ChannelConfig{Label: label, Ordered: false, MaxRetransmits: &zero}
}

// SemiReliableChannel 返回一个有限重传通道配置(最多重传 n 次)。
// 比完全可靠低延迟,比完全不可靠少丢包——适合游戏事件。
func SemiReliableChannel(label string, maxRetransmits int) ChannelConfig {
	return ChannelConfig{Label: label, Ordered: true, MaxRetransmits: &maxRetransmits}
}

// ─── 生命周期回调 ───

// Callbacks 信令服务器的生命周期回调。所有回调均在各自 goroutine 的上下文中同步调用,
// 应尽量轻量(重活请异步)。
type Callbacks struct {
	// OnConnectionRequest 在 WebSocket 升级之前调用。返回 false 拒绝连接(响应 403)。
	// r 是原始 HTTP 请求,可读取 header/cookie/query 做鉴权。
	// 默认允许所有连接。
	OnConnectionRequest func(r *http.Request) bool

	// OnPeerConnected peer 成功加入房间后调用。
	OnPeerConnected func(roomName string, peerID p2p.PeerID)

	// OnPeerDisconnected peer 离开房间后调用。
	OnPeerDisconnected func(roomName string, peerID p2p.PeerID)

	// OnRoomCreated 新房间创建时调用。
	OnRoomCreated func(roomName string)

	// OnRoomEmpty 房间最后一个人离开(房间即将销毁)时调用。
	OnRoomEmpty func(roomName string)
}

// ─── Server ───

// Server 是信令服务器。零值不可用,用 NewServer 构造。
type Server struct {
	topo      topology.Topology
	counter   atomic.Uint64
	idGen     func() string
	rooms     map[string]*room
	mu        sync.Mutex
	wsOpts    []ws.Option
	callbacks Callbacks
	channels  []ChannelConfig // 通道配置(发给每个客户端)
}

// Option 配置 Server。
type Option func(*Server)

// WithTopology 设置拓扑策略(默认 FullMesh)。
func WithTopology(t topology.Topology) Option {
	return func(s *Server) { s.topo = t }
}

// WithIDGenerator 设置 peer ID 生成器(默认用递增计数器)。
func WithIDGenerator(fn func() string) Option {
	return func(s *Server) { s.idGen = fn }
}

// WithWSOptions 透传 WebSocket 选项(如 WithOriginPatterns、WithPingInterval)。
func WithWSOptions(opts ...ws.Option) Option {
	return func(s *Server) { s.wsOpts = append(s.wsOpts, opts...) }
}

// WithCallbacks 设置生命周期回调。
func WithCallbacks(cb Callbacks) Option {
	return func(s *Server) { s.callbacks = cb }
}

// WithChannels 设置通道配置(会在 assign_id 时告知客户端应创建哪些 DataChannel)。
// 默认:一条 reliable + 一条 unreliable。
func WithChannels(channels ...ChannelConfig) Option {
	return func(s *Server) { s.channels = channels }
}

// WithConnectionAuth 设置连接鉴权钩子(WebSocket 升级前调用,返回 false 拒绝)。
// 这是 WithCallbacks 的快捷方式,只设置 OnConnectionRequest。
func WithConnectionAuth(fn func(r *http.Request) bool) Option {
	return func(s *Server) { s.callbacks.OnConnectionRequest = fn }
}

// NewServer 创建信令服务器。
func NewServer(opts ...Option) *Server {
	s := &Server{
		topo:  topology.FullMesh{},
		rooms: make(map[string]*room),
		channels: []ChannelConfig{
			ReliableChannel("reliable"),
			UnreliableChannel("unreliable"),
		},
	}
	s.idGen = s.defaultIDGen
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Server) defaultIDGen() string {
	n := s.counter.Add(1)
	return fmt.Sprintf("peer_%d", n)
}

// Handler 返回 http.HandlerFunc,挂载到路由即可。
//
// 支持 URL 查询参数:
//   - room: 房间名(备选,客户端也可在 join 消息中指定)
//   - next: 凑人数量(自动启用 WaitForN 拓扑覆盖)
//
// 示例:
//
//	mux.Handle("/ws", srv.Handler())
//	// 客户端连接: ws://host/ws?room=game1&next=4
func (s *Server) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 连接鉴权钩子:在 WS 升级之前执行
		if s.callbacks.OnConnectionRequest != nil {
			if !s.callbacks.OnConnectionRequest(r) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
		// 委托给 ws.Handler 做升级
		handler := ws.Handler(s.handle, s.wsOpts...)
		handler(w, r)
	}
}

func (s *Server) handle(r *http.Request, c *ws.Conn) error {
	ctx := r.Context()
	peer := &serverPeer{conn: c, ctx: ctx}

	// 尝试从 URL query 获取 room(备选路径)
	queryRoom := r.URL.Query().Get("room")
	queryNext := r.URL.Query().Get("next")

	// 等待客户端发 join 消息
	var env Envelope
	if err := c.ReadJSON(ctx, &env); err != nil {
		return err
	}
	if env.Event != EventJoin {
		return fmt.Errorf("signaling: expected %q, got %q", EventJoin, env.Event)
	}
	var join JoinPayload
	if err := json.Unmarshal(env.Data, &join); err != nil {
		return fmt.Errorf("signaling: bad join payload: %w", err)
	}

	// room 优先取 join payload,备选 URL query
	roomName := join.Room
	if roomName == "" {
		roomName = queryRoom
	}
	if roomName == "" {
		roomName = "default"
	}

	peerID := join.PeerID
	if peerID == "" {
		peerID = s.idGen()
	}
	peer.id = peerID

	// 发送 assign_id(含通道配置)
	if err := peer.sendEvent(EventAssignID, AssignIDPayload{
		PeerID:   peerID,
		Channels: s.channels,
	}); err != nil {
		return err
	}

	// 确定此连接使用的拓扑(如果 URL 有 ?next=N,覆盖全局拓扑)
	topo := s.topoForRequest(queryNext)

	// 加入房间
	rm := s.getOrCreateRoom(roomName)
	rm.join(peer, topo)
	if s.callbacks.OnPeerConnected != nil {
		s.callbacks.OnPeerConnected(roomName, peerID)
	}
	defer func() {
		rm.leave(peer, topo)
		if s.callbacks.OnPeerDisconnected != nil {
			s.callbacks.OnPeerDisconnected(roomName, peerID)
		}
		s.cleanRoom(roomName)
	}()

	// 消息循环
	for {
		var msg Envelope
		if err := c.ReadJSON(ctx, &msg); err != nil {
			return nil // 连接关闭视为正常离开
		}
		switch msg.Event {
		case EventRelay:
			var relay RelayPayload
			if err := json.Unmarshal(msg.Data, &relay); err != nil {
				slog.Debug("signaling: bad relay payload", "peer", peerID, "err", err)
				continue
			}
			rm.relay(peerID, relay)
		case EventPing:
			// KeepAlive:收到心跳后回复,防止反向代理关闭空闲连接
			_ = peer.sendEvent(EventKeepAlive, nil)
		case EventLeave:
			return nil
		default:
			slog.Debug("signaling: unknown event from client", "peer", peerID, "event", msg.Event)
		}
	}
}

// topoForRequest 根据 URL 参数决定是否覆盖拓扑。
func (s *Server) topoForRequest(queryNext string) topology.Topology {
	if queryNext != "" {
		if n, err := strconv.Atoi(queryNext); err == nil && n >= 2 {
			return topology.NewWaitForN(n)
		}
	}
	return s.topo
}

func (s *Server) getOrCreateRoom(name string) *room {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rm, ok := s.rooms[name]; ok {
		return rm
	}
	rm := &room{name: name, peers: make(map[p2p.PeerID]*serverPeer)}
	s.rooms[name] = rm
	if s.callbacks.OnRoomCreated != nil {
		s.callbacks.OnRoomCreated(name)
	}
	return rm
}

func (s *Server) cleanRoom(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rm, ok := s.rooms[name]; ok {
		rm.mu.RLock()
		empty := len(rm.peers) == 0
		rm.mu.RUnlock()
		if empty {
			delete(s.rooms, name)
			if s.callbacks.OnRoomEmpty != nil {
				s.callbacks.OnRoomEmpty(name)
			}
		}
	}
}

// RoomCount 返回当前活跃房间数(测试/运维用)。
func (s *Server) RoomCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms)
}

// PeerCount 返回指定房间的 peer 数;房间不存在返回 0。
func (s *Server) PeerCount(roomName string) int {
	s.mu.Lock()
	rm, ok := s.rooms[roomName]
	s.mu.Unlock()
	if !ok {
		return 0
	}
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.peers)
}

// ─── room ───

type room struct {
	name  string
	mu    sync.RWMutex
	peers map[p2p.PeerID]*serverPeer
}

func (rm *room) join(peer *serverPeer, topo topology.Topology) {
	rm.mu.Lock()
	existing := make([]p2p.PeerID, 0, len(rm.peers))
	for id := range rm.peers {
		existing = append(existing, id)
	}
	rm.peers[peer.id] = peer
	rm.mu.Unlock()

	pairs := topo.OnPeerJoin(peer.id, existing)

	rm.mu.RLock()
	defer rm.mu.RUnlock()
	for _, pair := range pairs {
		if p, ok := rm.peers[pair.Initiator]; ok {
			_ = p.sendEvent(EventPeerJoined, PeerJoinedPayload{
				PeerID:    pair.Responder,
				Initiator: true,
			})
		}
		if p, ok := rm.peers[pair.Responder]; ok {
			_ = p.sendEvent(EventPeerJoined, PeerJoinedPayload{
				PeerID:    pair.Initiator,
				Initiator: false,
			})
		}
	}
}

func (rm *room) leave(peer *serverPeer, topo topology.Topology) {
	rm.mu.Lock()
	delete(rm.peers, peer.id)
	remaining := make([]p2p.PeerID, 0, len(rm.peers))
	for id := range rm.peers {
		remaining = append(remaining, id)
	}
	rm.mu.Unlock()

	_ = topo.OnPeerLeave(peer.id, remaining)

	rm.mu.RLock()
	defer rm.mu.RUnlock()
	for _, p := range rm.peers {
		_ = p.sendEvent(EventPeerLeft, PeerLeftPayload{PeerID: peer.id})
	}
}

func (rm *room) relay(from p2p.PeerID, msg RelayPayload) {
	rm.mu.RLock()
	target, ok := rm.peers[msg.To]
	rm.mu.RUnlock()
	if !ok {
		slog.Debug("signaling: relay target not found", "from", from, "to", msg.To)
		return
	}
	_ = target.sendEvent(EventSignal, SignalPayload{From: from, Data: msg.Data})
}

// ─── serverPeer ───

type serverPeer struct {
	id   p2p.PeerID
	conn *ws.Conn
	ctx  context.Context
	mu   sync.Mutex
}

func (p *serverPeer) sendEvent(event string, data any) error {
	var payload json.RawMessage
	if data != nil {
		var err error
		payload, err = json.Marshal(data)
		if err != nil {
			return err
		}
	}
	env := Envelope{Event: event, Data: payload}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn.WriteJSON(p.ctx, env)
}
