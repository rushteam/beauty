// 路由压缩:把字符串路由映射为整数 Kind,减少线上包头开销。
//
// 游戏/移动端场景下,每条消息都带 route 字符串(如 "game.move", "chat.send")
// 对弱网/高频小包来说浪费带宽。RouteCodec 在握手时一次性下发 route→kind 映射表,
// 后续传输中用 2 字节 uint16 替代字符串。
//
// 支持两种模式:
//   - **静态注册**:启动时 Register 所有路由,映射固定不变;
//   - **动态扩展**:运行中 Register 新路由,调 Diff(lastVersion) 获取增量映射,
//     推送给客户端。
//
// 并发安全。
//
// 用法:
//
//	codec := router.NewRouteCodec()
//	codec.Register("game.move", "game.sync", "chat.send")
//
//	// 握手时下发完整映射
//	table := codec.Table()  // map[string]uint16
//	sendToClient(table)
//
//	// 编码
//	kind, ok := codec.Encode("game.move") // kind=1, ok=true
//	// 解码
//	route, ok := codec.Decode(kind)        // route="game.move", ok=true
package router

import "sync"

// RouteCodec 管理 route 字符串 ↔ uint16 kind 的双向映射。
// Kind 从 1 开始自增分配(0 保留)。
type RouteCodec struct {
	mu      sync.RWMutex
	toKind  map[string]uint16
	toRoute map[uint16]string
	next    uint16
	version uint64 // 每次 Register 递增
	history []versionEntry
}

type versionEntry struct {
	version uint64
	route   string
	kind    uint16
}

// NewRouteCodec 创建空的路由编解码器。
func NewRouteCodec() *RouteCodec {
	return &RouteCodec{
		toKind:  make(map[string]uint16),
		toRoute: make(map[uint16]string),
		next:    1,
	}
}

// Register 注册一个或多个路由。已存在的路由会跳过(不会改变 kind)。
// 返回本次新增的路由数量。
func (c *RouteCodec) Register(routes ...string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	added := 0
	for _, r := range routes {
		if _, ok := c.toKind[r]; ok {
			continue
		}
		kind := c.next
		c.next++
		c.toKind[r] = kind
		c.toRoute[kind] = r
		c.version++
		c.history = append(c.history, versionEntry{
			version: c.version,
			route:   r,
			kind:    kind,
		})
		added++
	}
	return added
}

// Encode 把 route 字符串编码为 kind。不存在返回 0, false。
func (c *RouteCodec) Encode(route string) (uint16, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	k, ok := c.toKind[route]
	return k, ok
}

// Decode 把 kind 解码为 route 字符串。不存在返回 "", false。
func (c *RouteCodec) Decode(kind uint16) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.toRoute[kind]
	return r, ok
}

// Len 返回当前已注册的路由数量。
func (c *RouteCodec) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.toKind)
}

// Version 返回当前版本号。每次新增路由版本 +1。
func (c *RouteCodec) Version() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// Table 返回当前完整映射表的快照(route→kind),适合在握手时下发给客户端。
func (c *RouteCodec) Table() map[string]uint16 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[string]uint16, len(c.toKind))
	for r, k := range c.toKind {
		m[r] = k
	}
	return m
}

// ReverseTable 返回 kind→route 的快照。
func (c *RouteCodec) ReverseTable() map[uint16]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[uint16]string, len(c.toRoute))
	for k, r := range c.toRoute {
		m[k] = r
	}
	return m
}

// Diff 返回自 sinceVersion 以来新增的映射。用于动态扩展场景:
// 客户端记录上次同步的版本号,服务端用 Diff 生成增量推送。
func (c *RouteCodec) Diff(sinceVersion uint64) map[string]uint16 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[string]uint16)
	for _, e := range c.history {
		if e.version > sinceVersion {
			m[e.route] = e.kind
		}
	}
	return m
}
