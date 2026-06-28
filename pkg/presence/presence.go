// Package presence 提供在线状态(在场追踪)原语:维护"谁在哪个流(stream)里"
// 的双向索引,支持 O(1) 按流查询成员、按会话查询所在流,以及 join/leave 事件总线。
//
// 适用场景:IM 频道成员、游戏房间在场、在线状态广播、匹配器候选池等。
//
// 双索引 presence 树:
//   - presencesByStream:  按 流 → 查成员(广播用)
//   - presencesBySession: 按 会话 → 查所在流(下线清理用)
// 一次 Track/Untrack 同步更新两棵索引,双向查询均 O(1)。
//
// 零值不可用,用 New 构造。Tracker 并发安全。
package presence

import (
	"context"
	"sync"
	"sync/atomic"
)

// Stream 标识一个逻辑频道/房间/频道,由业务自定义 Mode + Subject + Label。
// 同 Stream 值指向同一组成员。使用值类型以便做 map key。
type Stream struct {
	Mode    uint8  // 业务自定义模式(如 1=频道 2=房间 3=派对)
	Subject string // 主体标识(如房间 ID)
	Label   string // 子分类(如节点名或分区)
}

// ID 唯一标识一个在场条目:会话 + 所在流 + 所在节点。
// Node 为空表示本节点;非空表示该会话驻留在 Node 节点(跨节点路由用)。
type ID struct {
	SessionID string
	Stream    Stream
	Node      string
}

// Meta 是在场的元数据。
type Meta struct {
	UserID   string
	Username string
	Hidden   bool // 隐藏在场:不出现在成员列表,但仍被追踪(如观战)
}

// Presence 是一个完整的在场条目。
type Presence struct {
	ID   ID
	Meta Meta
}

// Event 是 join/leave 事件,由事件总线扇出给监听者。
type Event struct {
	Stream Stream
	Joins  []*Presence
	Leaves []*Presence
}

// Listener 监听某个流的 join/leave 事件。在事件总线 goroutine 中调用。
type Listener func(stream Stream, joins, leaves []*Presence)

// Tracker 维护在线状态的双索引与事件总线。
type Tracker struct {
	mu sync.RWMutex
	// 双索引:presencesByStream[stream][id] = Presence
	byStream  map[Stream]map[ID]*Presence
	bySession map[string]map[ID]*Presence
	count     atomic.Int64

	// 事件总线。
	eventsCh chan *Event
	listener Listener
	ctx      context.Context
	cancel   context.CancelFunc
	stopCh   chan struct{}
	done     chan struct{}
}

// New 创建 Tracker。listener 可为 nil(不订阅事件)。
// eventQueueSize 决定事件总线 channel 容量,满时 Track/Untrack 不阻塞(丢弃事件并计数)。
func New(listener Listener, eventQueueSize int) *Tracker {
	if eventQueueSize <= 0 {
		eventQueueSize = 256
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Tracker{
		byStream:  make(map[Stream]map[ID]*Presence),
		bySession: make(map[string]map[ID]*Presence),
		eventsCh:  make(chan *Event, eventQueueSize),
		listener:  listener,
		ctx:       ctx,
		cancel:    cancel,
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
	}
	if listener != nil {
		go t.dispatch()
	}
	return t
}

// Start 启动事件分发(若构造时给了 listener 则已在运行)。幂等。
func (t *Tracker) Start(ctx context.Context) {
	// dispatch 在 New 时已启动,这里只做 ctx 联动。
	go func() {
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.stopCh:
		}
	}()
}

// Stop 停止事件分发。幂等。停止后 Track/Untrack 仍可用(索引维护),只是不再回调。
func (t *Tracker) Stop() {
	select {
	case <-t.stopCh:
		return
	default:
		close(t.stopCh)
	}
	t.cancel()
	// 排空事件 channel 让 dispatch 退出。
	// 注意:dispatch 在 <-t.ctx.Done() 后会排空剩余事件再退出。
}

// Wait 阻塞直到事件分发 goroutine 退出。
func (t *Tracker) Wait() { <-t.done }

// Count 返回当前总在场数。
func (t *Tracker) Count() int { return int(t.count.Load()) }

// Track 登记一个会话在某流的在场。已存在则幂等返回 (true, false)。
// 返回 (added, newlyTracked):added=是否在流内(含已存在),newlyTracked=本次是否新增。
func (t *Tracker) Track(sessionID string, stream Stream, meta Meta) (bool, bool) {
	t.mu.Lock()
	id := ID{SessionID: sessionID, Stream: stream}
	p := &Presence{ID: id, Meta: meta}

	// 先查会话索引:是否已存在此条目。
	if bySess, ok := t.bySession[sessionID]; ok {
		if _, exists := bySess[id]; exists {
			t.mu.Unlock()
			return true, false
		}
		bySess[id] = p
	} else {
		t.bySession[sessionID] = map[ID]*Presence{id: p}
	}
	t.count.Add(1)

	// 更新流索引。
	byStr, ok := t.byStream[stream]
	if !ok {
		byStr = make(map[ID]*Presence)
		t.byStream[stream] = byStr
	}
	byStr[id] = p

	t.mu.Unlock()

	// 非隐藏条目发 join 事件。
	if !meta.Hidden {
		t.queueEvent(&Event{Stream: stream, Joins: []*Presence{p}})
	}
	return true, true
}

// Untrack 移除一个会话在某流的在场。返回是否曾存在。
func (t *Tracker) Untrack(sessionID string, stream Stream, userID string) bool {
	t.mu.Lock()
	id := ID{SessionID: sessionID, Stream: stream}
	bySess, ok := t.bySession[sessionID]
	if !ok {
		t.mu.Unlock()
		return false
	}
	p, exists := bySess[id]
	if !exists {
		t.mu.Unlock()
		return false
	}
	delete(bySess, id)
	if len(bySess) == 0 {
		delete(t.bySession, sessionID)
	}
	if byStr, ok := t.byStream[stream]; ok {
		delete(byStr, id)
		if len(byStr) == 0 {
			delete(t.byStream, stream)
		}
	}
	t.count.Add(-1)
	t.mu.Unlock()

	if !p.Meta.Hidden {
		t.queueEvent(&Event{Stream: stream, Leaves: []*Presence{p}})
	}
	return true
}

// UntrackAll 移除一个会话的所有在场(下线清理)。返回被移除的条目。
func (t *Tracker) UntrackAll(sessionID string) []*Presence {
	t.mu.Lock()
	bySess, ok := t.bySession[sessionID]
	if !ok {
		t.mu.Unlock()
		return nil
	}
	removed := make([]*Presence, 0, len(bySess))
	// 按 stream 聚合 leave 事件。
	leaveByStream := make(map[Stream][]*Presence)
	for id, p := range bySess {
		removed = append(removed, p)
		if byStr, ok := t.byStream[id.Stream]; ok {
			delete(byStr, id)
			if len(byStr) == 0 {
				delete(t.byStream, id.Stream)
			}
		}
		if !p.Meta.Hidden {
			leaveByStream[id.Stream] = append(leaveByStream[id.Stream], p)
		}
	}
	delete(t.bySession, sessionID)
	t.count.Add(int64(-len(removed)))
	t.mu.Unlock()

	for stream, leaves := range leaveByStream {
		t.queueEvent(&Event{Stream: stream, Leaves: leaves})
	}
	return removed
}

// ListByStream 返回某流的全部在场成员。includeHidden 控制是否含隐藏条目。
func (t *Tracker) ListByStream(stream Stream, includeHidden bool) []*Presence {
	t.mu.RLock()
	defer t.mu.RUnlock()
	byStr, ok := t.byStream[stream]
	if !ok {
		return nil
	}
	out := make([]*Presence, 0, len(byStr))
	for _, p := range byStr {
		if !p.Meta.Hidden || includeHidden {
			out = append(out, p)
		}
	}
	return out
}

// ListBySession 返回某会话所在的全部流。
func (t *Tracker) ListBySession(sessionID string) []*Presence {
	t.mu.RLock()
	defer t.mu.RUnlock()
	bySess, ok := t.bySession[sessionID]
	if !ok {
		return nil
	}
	out := make([]*Presence, 0, len(bySess))
	for _, p := range bySess {
		out = append(out, p)
	}
	return out
}

// CountByStream 返回某流的在场数。
func (t *Tracker) CountByStream(stream Stream) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.byStream[stream])
}

// StreamExists 返回某流是否有任何在场。
func (t *Tracker) StreamExists(stream Stream) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.byStream[stream]
	return ok
}

// GetLocal 返回单个条目(若存在)。
func (t *Tracker) GetLocal(sessionID string, stream Stream) (*Presence, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	id := ID{SessionID: sessionID, Stream: stream}
	if byStr, ok := t.byStream[stream]; ok {
		if p, ok := byStr[id]; ok {
			return p, true
		}
	}
	return nil, false
}

func (t *Tracker) queueEvent(e *Event) {
	select {
	case t.eventsCh <- e:
	default:
		// 事件队列满:丢弃(事件总线是尽力而为的,索引仍正确)。
	}
}

// dispatch 是事件分发 goroutine:从 eventsCh 取事件回调 listener。
func (t *Tracker) dispatch() {
	defer close(t.done)
	for {
		select {
		case <-t.ctx.Done():
			// 排空剩余事件再退出。
			for {
				select {
				case e := <-t.eventsCh:
					t.listener(e.Stream, e.Joins, e.Leaves)
				default:
					return
				}
			}
		case e := <-t.eventsCh:
			t.listener(e.Stream, e.Joins, e.Leaves)
		}
	}
}
