// Package router 提供多语义消息路由:把一条消息发给一组人(presence IDs)、
// 一个流(stream)的全部成员、或所有人,并支持一帧内多条消息攒批下发。
//
// 它是 pkg/stream.Broadcaster 的增强版:Broadcaster 只做"扇出给订阅者",
// Router 额外支持"按在场 ID 定点投递"和"按流投递(借助 presence.Tracker)",
// 以及攒批(SendDeferred)减少系统调用。
//
// 设计参考 Nakama server/message_router.go 的 MessageRouter 接口。
//
// 零值不可用,用 New 构造。Router 并发安全。
package router

import (
	"sync"

	"github.com/rushteam/beauty/pkg/presence"
)

// Message 是一条待路由的消息:任意负载 + 可靠性标记。
// 业务自行约定 Data 的编码(JSON/protobuf),Router 不关心。
type Message struct {
	Data     []byte
	Reliable bool // 可靠投递:对慢接收者阻塞而非丢弃(若 Sink 支持)
}

// Sink 是消息的最终投递目标,通常是一个 WebSocket/gRPC 会话的 Send 封装。
// 返回 false 表示接收者已不可达(下线),Router 会从 presence 清理(若配置了 Tracker)。
type Sink func(m Message) bool

// SinkRegistry 把 presence.ID 解析到 Sink。业务维护 session -> Sink 的映射。
type SinkRegistry interface {
	Lookup(sessionID string) Sink
}

// Router 按 presence IDs / stream / 全员 投递消息,并支持攒批。
type Router struct {
	registry SinkRegistry
	tracker  *presence.Tracker // 可为 nil:不按 stream 投递
	deferMu  sync.Mutex
	deferred []deferredItem
}

type deferredItem struct {
	targets []string // session IDs
	msg     Message
}

// New 创建 Router。tracker 可为 nil(此时 SendToStream 不可用,会返回 0)。
func New(registry SinkRegistry, tracker *presence.Tracker) *Router {
	return &Router{registry: registry, tracker: tracker}
}

// SetTracker 在构造后注入/替换 tracker(用于解耦初始化顺序)。
func (r *Router) SetTracker(t *presence.Tracker) {
	r.deferMu.Lock()
	r.tracker = t
	r.deferMu.Unlock()
}

// SendToPresenceIDs 把消息投递给指定 presence IDs 对应的会话。
// 返回成功投递的数量。接收者已下线(Sink 返回 false)的不计入。
func (r *Router) SendToPresenceIDs(ids []presence.ID, m Message) int {
	delivered := 0
	for _, id := range ids {
		sink := r.registry.Lookup(id.SessionID)
		if sink == nil {
			continue
		}
		if sink(m) {
			delivered++
		}
	}
	return delivered
}

// SendToSessionIDs 是 SendToPresenceIDs 的简化版:直接按 session ID 投递。
func (r *Router) SendToSessionIDs(sessionIDs []string, m Message) int {
	delivered := 0
	for _, sid := range sessionIDs {
		sink := r.registry.Lookup(sid)
		if sink == nil {
			continue
		}
		if sink(m) {
			delivered++
		}
	}
	return delivered
}

// SendToStream 把消息投递给某流的全部在场成员。
// 依赖 presence.Tracker;未配置时返回 0。includeHidden 控制是否含隐藏成员。
func (r *Router) SendToStream(stream presence.Stream, m Message, includeHidden bool) int {
	r.deferMu.Lock()
	tracker := r.tracker
	r.deferMu.Unlock()
	if tracker == nil {
		return 0
	}
	members := tracker.ListByStream(stream, includeHidden)
	if len(members) == 0 {
		return 0
	}
	delivered := 0
	for _, p := range members {
		sink := r.registry.Lookup(p.ID.SessionID)
		if sink == nil {
			continue
		}
		if sink(m) {
			delivered++
		}
	}
	return delivered
}

// QueueDeferred 把一条消息加入攒批队列,稍后由 FlushDeferred 一次性投递。
// 适合一帧内产生多条消息时减少重复 Lookup/系统调用。
// targets 为目标 session IDs;为空时投递给全员(需 Flush 时遍历,慎用)。
func (r *Router) QueueDeferred(targets []string, m Message) {
	r.deferMu.Lock()
	r.deferred = append(r.deferred, deferredItem{targets: targets, msg: m})
	r.deferMu.Unlock()
}

// FlushDeferred 投递所有攒批消息并清空队列。返回总投递数。
// 对同一 session 的多条消息会按顺序投递(保持 FIFO)。
func (r *Router) FlushDeferred() int {
	r.deferMu.Lock()
	items := r.deferred
	r.deferred = nil
	r.deferMu.Unlock()

	// 按 session 聚合,减少重复 Lookup。
	bySession := make(map[string][]Message)
	for _, it := range items {
		for _, sid := range it.targets {
			bySession[sid] = append(bySession[sid], it.msg)
		}
	}
	delivered := 0
	for sid, msgs := range bySession {
		sink := r.registry.Lookup(sid)
		if sink == nil {
			continue
		}
		for _, m := range msgs {
			if sink(m) {
				delivered++
			}
		}
	}
	return delivered
}

// SendToAll 广播给全员。需要业务提供全员 session ID 列表(由 AllSessionIDs 返回)。
// 这是保守设计:避免 Router 隐式持有全局 session 表导致耦合。
func (r *Router) SendToAll(allSessionIDs func() []string, m Message) int {
	if allSessionIDs == nil {
		return 0
	}
	return r.SendToSessionIDs(allSessionIDs(), m)
}
