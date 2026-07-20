// Package status 把用户的在线状态变化(上线/下线/进出流)推送给关注他的人。
//
// 它是 pkg/presence 的跨流编排层:presence.Listener 只在某流内广播 join/leave
// 给同流成员;本包订阅 presence 事件,查"谁关注了状态变化的人"(走 relationship
// 的反向查询 Watchers),再把 status notification 走 router 投递给关注者的会话。
//
// status event:用户上下线/进出流时,
// 服务端自动给关注者推一条 status notification(不只是一条 join/leave,而是
// "这个朋友现在在线/在哪个频道")。
//
// 依赖三组件,均通过接口注入,便于测试与解耦:
//   - presenceStore:查用户当前在哪些流(用于"在哪个频道");
//   - watcherFinder:查谁关注了某用户(Watchers);
//   - notifier:把 status 消息投递给关注者会话(router.SendToSessionIDs 的封装)。
//
// 零值不可用,用 New 构造。并发安全(只组合并发安全组件 + 自身 mutex)。
package status

import (
	"sync"

	"github.com/rushteam/beauty/pkg/presence"
)

// State 用户在线状态变化类型。
type State int

const (
	StateOnline  State = iota // 用户上线(任一流首次加入)
	StateOffline              // 用户下线(所有流离开)
)

// String 状态的可读名,用于日志/调试。
func (s State) String() string {
	switch s {
	case StateOnline:
		return "online"
	case StateOffline:
		return "offline"
	}
	return "unknown"
}

// Notification 一条 status 通知:谁 + 状态 + 他现在在哪些流。
type Notification struct {
	UserID  string
	State   State
	Streams []presence.Stream // StateOffline 时为 nil
}

// presenceStore 查用户当前所在的流(接口,避免循环依赖直接引用 presence.Tracker)。
type presenceStore interface {
	ListBySession(sessionID string) []*presence.Presence
}

// watcherFinder 反向查询"谁关注了 userID"。
type watcherFinder interface {
	Watchers(userID string, stateFilter int) []string
}

// Notifier 把 payload 投递给一组 sessionID。业务用 adapter 桥接 router:
//
//	status.WithNotifier(func(sids []string, p []byte) int {
//	    return router.SendToSessionIDs(sids, router.Message{Data: p, Reliable: true})
//	})
type Notifier func(sessionIDs []string, payload []byte) int

// Encoder 把 Notification 编码为 payload。默认 JSON;业务可注入自定义编码。
type Encoder func(n Notification) []byte

// Dispatcher 订阅 presence 事件并分发 status notification。
type Dispatcher struct {
	store   presenceStore
	finders []watcherFinder // 多个图谱可叠加(如好友 + 关注分属不同图)
	notify  Notifier
	encode  Encoder

	// 用户 → 当前所在流集合(用于判断 online/offline 转换)。
	mu     sync.Mutex
	online map[string]map[presence.Stream]struct{}
}

// Option 配置 Dispatcher。
type Option func(*config)

type config struct {
	store   presenceStore
	finders []watcherFinder
	notify  Notifier
	encode  Encoder
}

// WithPresenceStore 设置在场查询源(必填,通常 *presence.Tracker)。
func WithPresenceStore(s presenceStore) Option { return func(c *config) { c.store = s } }

// WithWatcherFinder 添加一个关注者查找器(可多次调用,叠加多个图谱)。
// 传入的必须实现 Watchers(userID, stateFilter) []string —— relationship.Graph 已实现。
func WithWatcherFinder(f watcherFinder) Option {
	return func(c *config) { c.finders = append(c.finders, f) }
}

// WithNotifier 设置投递器(必填)。业务用 adapter 桥接 router:
//
//	status.WithNotifier(func(sids []string, p []byte) int {
//	    return router.SendToSessionIDs(sids, router.Message{Data: p, Reliable: true})
//	})
func WithNotifier(n Notifier) Option { return func(c *config) { c.notify = n } }

// WithEncoder 设置自定义编码(默认 JSON)。
func WithEncoder(e Encoder) Option { return func(c *config) { c.encode = e } }

// New 创建 Dispatcher。store/notify 必填,finders 至少一个。
func New(opts ...Option) *Dispatcher {
	cfg := &config{encode: defaultEncode}
	for _, o := range opts {
		o(cfg)
	}
	return &Dispatcher{
		store:   cfg.store,
		finders: cfg.finders,
		notify:  cfg.notify,
		encode:  cfg.encode,
		online:  make(map[string]map[presence.Stream]struct{}),
	}
}

// OnPresence 是 presence.Listener 的适配器:把 presence 事件转成 status 事件。
// 用法:presence.New(dispatcher.OnPresence, queueSize)。
// 本方法在 presence 的事件总线 goroutine 中调用,线程安全。
func (d *Dispatcher) OnPresence(stream presence.Stream, joins, leaves []*presence.Presence) {
	// 处理 joins:对每个新加入的 userID,若之前不在线则转 online。
	for _, p := range joins {
		if p.Meta.UserID == "" {
			continue
		}
		d.handleJoin(p.Meta.UserID, stream)
	}
	// 处理 leaves:对每个离开的 userID,从该流移除;若所有流都离开则转 offline。
	for _, p := range leaves {
		if p.Meta.UserID == "" {
			continue
		}
		d.handleLeave(p.Meta.UserID, stream)
	}
}

// Dispatch 手动触发一次 status 通知(不经过 presence 事件,直接指定)。
// 用于业务主动通知"某用户上线/下线"。
func (d *Dispatcher) Dispatch(userID string, state State, streams []presence.Stream) {
	d.notifyWatchers(userID, state, streams)
}

func (d *Dispatcher) handleJoin(userID string, stream presence.Stream) {
	d.mu.Lock()
	streams := d.online[userID]
	if streams == nil {
		streams = make(map[presence.Stream]struct{})
		d.online[userID] = streams
	}
	wasEmpty := len(streams) == 0
	streams[stream] = struct{}{}
	d.mu.Unlock()
	if wasEmpty {
		// 从无到有:转 online。
		d.notifyWatchers(userID, StateOnline, d.userStreams(userID))
	}
}

func (d *Dispatcher) handleLeave(userID string, stream presence.Stream) {
	d.mu.Lock()
	streams := d.online[userID]
	if streams == nil {
		d.mu.Unlock()
		return
	}
	delete(streams, stream)
	if len(streams) > 0 {
		d.mu.Unlock()
		return
	}
	delete(d.online, userID)
	d.mu.Unlock()
	// 从有到无:转 offline。
	d.notifyWatchers(userID, StateOffline, nil)
}

// userStreams 查用户当前所有流(经 presenceStore 查 sessionID 的所有在场条目)。
// 注意:presenceStore.ListBySession 按 sessionID 查,而 status 按 userID 聚合——
// 同一用户可能有多个 session(多端登录),本实现通过 online map 已聚合,直接返回。
func (d *Dispatcher) userStreams(userID string) []presence.Stream {
	d.mu.Lock()
	defer d.mu.Unlock()
	streams := d.online[userID]
	if len(streams) == 0 {
		return nil
	}
	out := make([]presence.Stream, 0, len(streams))
	for s := range streams {
		out = append(out, s)
	}
	return out
}

func (d *Dispatcher) notifyWatchers(userID string, state State, streams []presence.Stream) {
	if d.notify == nil || len(d.finders) == 0 {
		return
	}
	// 收集所有图谱中关注 userID 的人(去重)。
	seen := make(map[string]struct{})
	var watchers []string
	for _, f := range d.finders {
		for _, w := range f.Watchers(userID, -1) {
			if _, ok := seen[w]; ok {
				continue
			}
			seen[w] = struct{}{}
			watchers = append(watchers, w)
		}
	}
	if len(watchers) == 0 {
		return
	}
	// 关注者是 userID,需要业务侧把 userID → sessionID 映射后才能投递。
	// 本包通过 WatcherResolver 接口把 userID 解析为 sessionID。
	// 若未配置 resolver,则直接用 userID 作 sessionID(适合 userID==sessionID 的简单场景)。
	sessionIDs := d.resolveSessions(watchers)
	if len(sessionIDs) == 0 {
		return
	}
	payload := d.encode(Notification{UserID: userID, State: state, Streams: streams})
	d.notify(sessionIDs, payload)
}

// WatcherResolver 把关注者 userID 解析为可投递的 sessionID 列表。
// 一个用户可能多端在线,返回多个 sessionID。
type WatcherResolver interface {
	Sessions(userID string) []string
}

// resolver 注入式 userID→sessionID 解析(可选)。
var resolver WatcherResolver

// SetResolver 设置全局 userID→sessionID 解析器(用于多端登录场景)。
// 简单场景(userID 即 sessionID)无需设置。
func SetResolver(r WatcherResolver) { resolver = r }

func (d *Dispatcher) resolveSessions(userIDs []string) []string {
	if resolver == nil {
		// 简单场景:userID 即 sessionID。
		return userIDs
	}
	var out []string
	for _, uid := range userIDs {
		out = append(out, resolver.Sessions(uid)...)
	}
	return out
}

// defaultEncode 默认 JSON 编码(避免引入 encoding/json 让业务可选)。
// 这里用最简 JSON 字符串,业务通常用 WithEncoder 注入自己的编码。
func defaultEncode(n Notification) []byte {
	return []byte(`{"user":"` + n.UserID + `","state":"` + n.State.String() + `"}`)
}
