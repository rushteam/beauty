// Package quadtree 提供自适应四叉树空间索引,用于实体密度不均匀的 2D 场景。
//
// 与同级 spatial 包(均匀网格)的区别:
//   - 均匀网格:当实体分布均匀时性能最优,但如果地图一角聚集大量玩家(如主城),
//     该网格单元退化为全量遍历。
//   - 四叉树:递归细分拥挤区域、合并空旷区域,自动适应密度分布。代价是维护成本
//     略高(插入/删除 O(log N))和锁粒度更粗,但范围查询在密度不均场景下远优。
//
// 适用:MMO 主城/野外混合场景、大逃杀(安全区收缩时玩家聚集)、2D 射击弹幕检测、
// SLG 大地图单元可见性查询。
//
// 泛型 ID 为实体标识(comparable)。坐标 float64。
// 并发安全(单读写锁)。零值不可用,用 New 构造。
package quadtree

import (
	"math"
	"sort"
	"sync"
)

// Rect 轴对齐矩形边界(左下角 + 宽高)。
type Rect struct {
	X, Y, W, H float64
}

func (r Rect) contains(x, y float64) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

func (r Rect) intersectsCircle(cx, cy, radius float64) bool {
	closestX := math.Max(r.X, math.Min(cx, r.X+r.W))
	closestY := math.Max(r.Y, math.Min(cy, r.Y+r.H))
	dx, dy := closestX-cx, closestY-cy
	return dx*dx+dy*dy <= radius*radius
}

func (r Rect) intersectsRect(o Rect) bool {
	return r.X < o.X+o.W && r.X+r.W > o.X && r.Y < o.Y+o.H && r.Y+r.H > o.Y
}

// Entity 查询返回的实体信息。
type Entity[ID comparable] struct {
	ID   ID
	X, Y float64
	Dist float64
}

type entry[ID comparable] struct {
	id   ID
	x, y float64
}

type node[ID comparable] struct {
	bounds   Rect
	entries  []entry[ID]
	children [4]*node[ID] // NW, NE, SW, SE
	divided  bool
}

func (n *node[ID]) isLeaf() bool { return !n.divided }

func (n *node[ID]) subdivide() {
	hw, hh := n.bounds.W/2, n.bounds.H/2
	x, y := n.bounds.X, n.bounds.Y
	n.children[0] = &node[ID]{bounds: Rect{x, y + hh, hw, hh}}      // NW
	n.children[1] = &node[ID]{bounds: Rect{x + hw, y + hh, hw, hh}} // NE
	n.children[2] = &node[ID]{bounds: Rect{x, y, hw, hh}}           // SW
	n.children[3] = &node[ID]{bounds: Rect{x + hw, y, hw, hh}}      // SE
	n.divided = true
}

func (n *node[ID]) childFor(x, y float64) int {
	midX := n.bounds.X + n.bounds.W/2
	midY := n.bounds.Y + n.bounds.H/2
	switch {
	case x < midX && y >= midY:
		return 0 // NW
	case x >= midX && y >= midY:
		return 1 // NE
	case x < midX && y < midY:
		return 2 // SW
	default:
		return 3 // SE
	}
}

// Tree 四叉树空间索引。零值不可用,用 New 构造。并发安全。
type Tree[ID comparable] struct {
	mu       sync.RWMutex
	root     *node[ID]
	capacity int // 每个叶节点的最大容量
	maxDepth int
	index    map[ID]entry[ID]
}

// New 创建四叉树。bounds 定义地图总边界,capacity 为节点分裂阈值(推荐 8~16),
// maxDepth 限制递归深度(防止退化,推荐 8~12)。
func New[ID comparable](bounds Rect, capacity, maxDepth int) *Tree[ID] {
	if capacity <= 0 {
		capacity = 8
	}
	if maxDepth <= 0 {
		maxDepth = 10
	}
	return &Tree[ID]{
		root:     &node[ID]{bounds: bounds},
		capacity: capacity,
		maxDepth: maxDepth,
		index:    make(map[ID]entry[ID]),
	}
}

// Add 添加实体。已存在则等同 Move。
func (t *Tree[ID]) Add(id ID, x, y float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.index[id]; ok {
		t.removeLocked(old)
	}
	e := entry[ID]{id: id, x: x, y: y}
	t.index[id] = e
	t.insertLocked(t.root, e, 0)
}

// Move 移动实体到新坐标。不存在则新增。
func (t *Tree[ID]) Move(id ID, x, y float64) { t.Add(id, x, y) }

// Remove 删除实体。不存在则无操作。
func (t *Tree[ID]) Remove(id ID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.index[id]; ok {
		t.removeLocked(old)
		delete(t.index, id)
	}
}

func (t *Tree[ID]) insertLocked(n *node[ID], e entry[ID], depth int) {
	if !n.bounds.contains(e.x, e.y) {
		return
	}
	if n.isLeaf() {
		if len(n.entries) < t.capacity || depth >= t.maxDepth {
			n.entries = append(n.entries, e)
			return
		}
		n.subdivide()
		old := n.entries
		n.entries = nil
		for _, o := range old {
			ci := n.childFor(o.x, o.y)
			t.insertLocked(n.children[ci], o, depth+1)
		}
	}
	ci := n.childFor(e.x, e.y)
	t.insertLocked(n.children[ci], e, depth+1)
}

func (t *Tree[ID]) removeLocked(e entry[ID]) {
	t.removeFromNode(t.root, e)
}

func (t *Tree[ID]) removeFromNode(n *node[ID], e entry[ID]) bool {
	if !n.bounds.contains(e.x, e.y) {
		return false
	}
	if n.isLeaf() {
		for i, ent := range n.entries {
			if ent.id == e.id {
				n.entries[i] = n.entries[len(n.entries)-1]
				n.entries = n.entries[:len(n.entries)-1]
				return true
			}
		}
		return false
	}
	ci := n.childFor(e.x, e.y)
	if t.removeFromNode(n.children[ci], e) {
		t.tryCollapse(n)
		return true
	}
	return false
}

func (t *Tree[ID]) tryCollapse(n *node[ID]) {
	if n.isLeaf() {
		return
	}
	total := 0
	for _, child := range n.children {
		if !child.isLeaf() {
			return
		}
		total += len(child.entries)
	}
	if total > t.capacity {
		return
	}
	n.entries = make([]entry[ID], 0, total)
	for _, child := range n.children {
		n.entries = append(n.entries, child.entries...)
	}
	n.children = [4]*node[ID]{}
	n.divided = false
}

// Pos 返回实体当前坐标。不存在返回 (0,0,false)。
func (t *Tree[ID]) Pos(id ID) (x, y float64, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if e, found := t.index[id]; found {
		return e.x, e.y, true
	}
	return 0, 0, false
}

// Len 返回实体总数。
func (t *Tree[ID]) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.index)
}

// Nearby 返回距 (x,y) 半径 radius 内的所有实体(含边界),按距离升序。
func (t *Tree[ID]) Nearby(x, y, radius float64, exclude ...ID) []Entity[ID] {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ex := toSet(exclude)
	var out []Entity[ID]
	r2 := radius * radius
	t.queryCircle(t.root, x, y, radius, r2, ex, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].Dist < out[j].Dist })
	return out
}

// KNN 返回距 (x,y) 最近的 k 个实体。radius 限定搜索范围(<=0 不限)。
func (t *Tree[ID]) KNN(x, y float64, k int, radius float64, exclude ...ID) []Entity[ID] {
	if k <= 0 {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	ex := toSet(exclude)
	var out []Entity[ID]
	if radius > 0 {
		r2 := radius * radius
		t.queryCircle(t.root, x, y, radius, r2, ex, &out)
	} else {
		t.queryAll(t.root, x, y, ex, &out)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dist < out[j].Dist })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// QueryRect 返回矩形区域内所有实体。
func (t *Tree[ID]) QueryRect(r Rect, exclude ...ID) []Entity[ID] {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ex := toSet(exclude)
	var out []Entity[ID]
	t.queryRect(t.root, r, ex, &out)
	return out
}

func (t *Tree[ID]) queryCircle(n *node[ID], cx, cy, radius, r2 float64, ex map[ID]struct{}, out *[]Entity[ID]) {
	if !n.bounds.intersectsCircle(cx, cy, radius) {
		return
	}
	if n.isLeaf() {
		for _, e := range n.entries {
			if _, skip := ex[e.id]; skip {
				continue
			}
			dx, dy := e.x-cx, e.y-cy
			d2 := dx*dx + dy*dy
			if d2 <= r2 {
				*out = append(*out, Entity[ID]{ID: e.id, X: e.x, Y: e.y, Dist: math.Sqrt(d2)})
			}
		}
		return
	}
	for _, child := range n.children {
		t.queryCircle(child, cx, cy, radius, r2, ex, out)
	}
}

func (t *Tree[ID]) queryRect(n *node[ID], r Rect, ex map[ID]struct{}, out *[]Entity[ID]) {
	if !n.bounds.intersectsRect(r) {
		return
	}
	if n.isLeaf() {
		for _, e := range n.entries {
			if _, skip := ex[e.id]; skip {
				continue
			}
			if r.contains(e.x, e.y) {
				*out = append(*out, Entity[ID]{ID: e.id, X: e.x, Y: e.y})
			}
		}
		return
	}
	for _, child := range n.children {
		t.queryRect(child, r, ex, out)
	}
}

func (t *Tree[ID]) queryAll(n *node[ID], cx, cy float64, ex map[ID]struct{}, out *[]Entity[ID]) {
	if n.isLeaf() {
		for _, e := range n.entries {
			if _, skip := ex[e.id]; skip {
				continue
			}
			dx, dy := e.x-cx, e.y-cy
			*out = append(*out, Entity[ID]{ID: e.id, X: e.x, Y: e.y, Dist: math.Sqrt(dx*dx + dy*dy)})
		}
		return
	}
	for _, child := range n.children {
		t.queryAll(child, cx, cy, ex, out)
	}
}

func toSet[ID comparable](ids []ID) map[ID]struct{} {
	if len(ids) == 0 {
		return nil
	}
	m := make(map[ID]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}
