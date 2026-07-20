// Package relationship 提供社交图谱原语:用二部有向图刻画"谁对谁是什么关系",
// 支持好友(双向)、关注(单向)、拉黑(单向隔离)、群组成员(带角色)等多种语义,
// 通过状态编码 + position 游标实现高效查询与分页。
//
// 设计要点:
//   - 边模型:edge = (source, destination, state, position, metadata);
//   - state 用 int 编码:数值大小即权限级别(无 RBAC 开销),如 0=成员 1=admin 2=owner;
//   - 单向 block 与好友关系共存:block 时删除对方非 block 边,好友请求前检查 block;
//   - position = 创建时间(UnixNano)作游标,支持百万级列表不中断分页。
//
// 适用场景:好友/关注/拉黑、群组成员与角色、任意"实体间带状态的关系"图谱。
//
// 零值不可用,用 New 构造。Graph 并发安全。
package relationship

import (
	"errors"
	"maps"
	"sort"
	"sync"
)

// Edge 一条有向关系边。
type Edge struct {
	Source      string            // 关系发起者
	Destination string            // 关系目标
	State       int               // 状态/角色(业务自定义:0=active,1=admin...;block 用 StateBlocked)
	Position    int64             // 游标(创建时间 nano),用于分页
	Metadata    map[string]string // 任意附加字段
}

// 常用状态值(业务可自定义扩展)。
const (
	StateActive  = 0  // 活跃关系(好友/成员)
	StatePending = 1  // 待确认(好友请求已发)
	StateAdmin   = 2  // 管理员(群组角色)
	StateOwner   = 3  // 拥有者(群组角色)
	StateBlocked = 99 // 拉黑(单向隔离)
)

// Graph 社交关系图。
type Graph struct {
	mu    sync.Mutex
	edges map[string]map[string]*Edge // source -> dest -> Edge
}

// ErrAlreadyExists 边已存在。
var ErrAlreadyExists = errors.New("relationship: edge already exists")

// ErrNotFound 边不存在。
var ErrNotFound = errors.New("relationship: edge not found")

// ErrBlocked 存在拉黑关系,操作被拒。
var ErrBlocked = errors.New("relationship: blocked")

// New 创建空图。
func New() *Graph {
	return &Graph{edges: make(map[string]map[string]*Edge)}
}

// AddEdge 添加一条有向边。position 为分页游标(建议用创建时间 nano)。
// 若 Source 被 Destination 拉黑,拒绝(好友请求前检查 block)。
func (g *Graph) AddEdge(e Edge) error {
	if e.Source == "" || e.Destination == "" {
		return errors.New("relationship: empty source/destination")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// 检查目标是否拉黑了自己。
	if dest := g.edges[e.Destination]; dest != nil {
		if b, ok := dest[e.Source]; ok && b.State == StateBlocked {
			return ErrBlocked
		}
	}
	if g.edges[e.Source] == nil {
		g.edges[e.Source] = make(map[string]*Edge)
	}
	if _, ok := g.edges[e.Source][e.Destination]; ok {
		return ErrAlreadyExists
	}
	cp := e
	if cp.Metadata != nil {
		cp.Metadata = cloneMap(e.Metadata)
	}
	g.edges[e.Source][e.Destination] = &cp
	return nil
}

// RemoveEdge 删除一条有向边。
func (g *Graph) RemoveEdge(source, dest string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.edges[source]; m != nil {
		if _, ok := m[dest]; ok {
			delete(m, dest)
			if len(m) == 0 {
				delete(g.edges, source)
			}
			return nil
		}
	}
	return ErrNotFound
}

// Block 拉黑:建立单向 block 边,并删除自己对对方的非 block 边。
func (g *Graph) Block(source, dest string, position int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.edges[source] == nil {
		g.edges[source] = make(map[string]*Edge)
	}
	// 删除自己对对方的现有非 block 边。
	if e, ok := g.edges[source][dest]; ok && e.State != StateBlocked {
		delete(g.edges[source], dest)
	}
	g.edges[source][dest] = &Edge{
		Source: source, Destination: dest, State: StateBlocked, Position: position,
	}
	return nil
}

// IsBlocked 判断 source 是否拉黑了 dest(单向)。
func (g *Graph) IsBlocked(source, dest string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.edges[source]; m != nil {
		if e, ok := m[dest]; ok && e.State == StateBlocked {
			return true
		}
	}
	return false
}

// AddFriend 双向好友:同时加 source→dest 和 dest→source 两条 active 边。
// 若任一方拉黑对方则拒绝。已存在则报错。
func (g *Graph) AddFriend(a, b string, position int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.isBlockedLocked(a, b) || g.isBlockedLocked(b, a) {
		return ErrBlocked
	}
	if err := g.addEdgeLocked(a, b, StateActive, position, nil); err != nil {
		return err
	}
	if err := g.addEdgeLocked(b, a, StateActive, position, nil); err != nil {
		// 回滚第一条。
		delete(g.edges[a], b)
		return err
	}
	return nil
}

// RemoveFriend 删除双向好友边。
func (g *Graph) RemoveFriend(a, b string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removeEdgeLocked(a, b)
	g.removeEdgeLocked(b, a)
}

// Outgoing 查询 source 指向的所有边(关注/好友/群组成员)。
// afterPosition 游标分页:返回 position < afterPosition 的(倒序,即较新的)。
// 若 afterPosition=0 返回全部(按 position 降序)。limit<=0 默认 50。
// stateFilter==-1 表示不过滤;否则只返回该 state。
func (g *Graph) Outgoing(source string, afterPosition int64, limit int, stateFilter int) []Edge {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.edges[source]
	if len(m) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	out := make([]Edge, 0, len(m))
	for _, e := range m {
		if stateFilter != -1 && e.State != stateFilter {
			continue
		}
		if afterPosition > 0 && e.Position >= afterPosition {
			continue
		}
		out = append(out, *e)
	}
	// 按 position 降序(较新在前)。
	sort.Slice(out, func(i, j int) bool { return out[i].Position > out[j].Position })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Friends 查询 source 的双向好友(取交集)。
func (g *Graph) Friends(source string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.edges[source]
	if len(m) == 0 {
		return nil
	}
	var out []string
	for dest, e := range m {
		if e.State != StateActive {
			continue
		}
		// 检查反向是否也是 active。
		if rev, ok := g.edges[dest]; ok && rev[source] != nil && rev[source].State == StateActive {
			out = append(out, dest)
		}
	}
	return out
}

// Watchers 反向查询:谁把 userID 作为 destination 建立了 active 边(关注者/好友)。
// 用于"用户上下线时通知谁"——即 status event 的订阅者发现。
// stateFilter==-1 不过滤;否则只返回该 state(如 StateActive=好友/关注,不含 block)。
func (g *Graph) Watchers(userID string, stateFilter int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for source, m := range g.edges {
		if e, ok := m[userID]; ok && e.State != StateBlocked {
			if stateFilter != -1 && e.State != stateFilter {
				continue
			}
			out = append(out, source)
		}
	}
	return out
}

// Edge 查询单条边。不存在返回 ErrNotFound。
func (g *Graph) Edge(source, dest string) (Edge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.edges[source]; m != nil {
		if e, ok := m[dest]; ok {
			return *e, nil
		}
	}
	return Edge{}, ErrNotFound
}

// Count source 的出边数(stateFilter==-1 不过滤)。
func (g *Graph) Count(source string, stateFilter int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.edges[source]
	if len(m) == 0 {
		return 0
	}
	if stateFilter == -1 {
		return len(m)
	}
	n := 0
	for _, e := range m {
		if e.State == stateFilter {
			n++
		}
	}
	return n
}

func (g *Graph) isBlockedLocked(source, dest string) bool {
	if m := g.edges[source]; m != nil {
		if e, ok := m[dest]; ok && e.State == StateBlocked {
			return true
		}
	}
	return false
}

func (g *Graph) addEdgeLocked(source, dest string, state int, pos int64, meta map[string]string) error {
	if g.edges[source] == nil {
		g.edges[source] = make(map[string]*Edge)
	}
	if _, ok := g.edges[source][dest]; ok {
		return ErrAlreadyExists
	}
	e := &Edge{Source: source, Destination: dest, State: state, Position: pos, Metadata: meta}
	if meta != nil {
		e.Metadata = cloneMap(meta)
	}
	g.edges[source][dest] = e
	return nil
}

func (g *Graph) removeEdgeLocked(source, dest string) {
	if m := g.edges[source]; m != nil {
		delete(m, dest)
		if len(m) == 0 {
			delete(g.edges, source)
		}
	}
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}
