// Package octree 提供自适应八叉树 3D 空间索引,用于体素世界中的实体 AOI。
//
// 与同级 quadtree 包的区别:
//   - quadtree 是 2D 四叉树,适用于平面场景(MMO、SLG)
//   - octree 是 3D 八叉树,适用于有高度维度的场景(体素世界、太空、飞行)
//
// 递归细分拥挤区域、合并空旷区域,自动适应实体密度分布。
// 泛型 ID 为实体标识(comparable)。坐标 float64。
// 并发安全(单读写锁)。零值不可用,用 New 构造。
package octree

import (
	"math"
	"sort"
	"sync"
)

// Box 轴对齐包围盒(原点 + 尺寸)。
type Box struct {
	X, Y, Z float64
	W, H, D float64 // Width, Height, Depth
}

func (b Box) contains(x, y, z float64) bool {
	return x >= b.X && x < b.X+b.W &&
		y >= b.Y && y < b.Y+b.H &&
		z >= b.Z && z < b.Z+b.D
}

func (b Box) intersectsSphere(cx, cy, cz, radius float64) bool {
	dx := math.Max(b.X-cx, math.Max(0, cx-(b.X+b.W)))
	dy := math.Max(b.Y-cy, math.Max(0, cy-(b.Y+b.H)))
	dz := math.Max(b.Z-cz, math.Max(0, cz-(b.Z+b.D)))
	return dx*dx+dy*dy+dz*dz <= radius*radius
}

func (b Box) intersectsBox(o Box) bool {
	return b.X < o.X+o.W && b.X+b.W > o.X &&
		b.Y < o.Y+o.H && b.Y+b.H > o.Y &&
		b.Z < o.Z+o.D && b.Z+b.D > o.Z
}

// Entity 查询返回的实体信息。
type Entity[ID comparable] struct {
	ID      ID
	X, Y, Z float64
	Dist    float64
}

type entry[ID comparable] struct {
	id      ID
	x, y, z float64
}

type node[ID comparable] struct {
	bounds   Box
	entries  []entry[ID]
	children [8]*node[ID]
	divided  bool
}

func (n *node[ID]) isLeaf() bool { return !n.divided }

func (n *node[ID]) subdivide() {
	hw, hh, hd := n.bounds.W/2, n.bounds.H/2, n.bounds.D/2
	x, y, z := n.bounds.X, n.bounds.Y, n.bounds.Z
	// 8 个子节点:低位=X, 中位=Y, 高位=Z
	n.children[0] = &node[ID]{bounds: Box{x, y, z, hw, hh, hd}}
	n.children[1] = &node[ID]{bounds: Box{x + hw, y, z, hw, hh, hd}}
	n.children[2] = &node[ID]{bounds: Box{x, y + hh, z, hw, hh, hd}}
	n.children[3] = &node[ID]{bounds: Box{x + hw, y + hh, z, hw, hh, hd}}
	n.children[4] = &node[ID]{bounds: Box{x, y, z + hd, hw, hh, hd}}
	n.children[5] = &node[ID]{bounds: Box{x + hw, y, z + hd, hw, hh, hd}}
	n.children[6] = &node[ID]{bounds: Box{x, y + hh, z + hd, hw, hh, hd}}
	n.children[7] = &node[ID]{bounds: Box{x + hw, y + hh, z + hd, hw, hh, hd}}
	n.divided = true
}

func (n *node[ID]) childFor(x, y, z float64) int {
	midX := n.bounds.X + n.bounds.W/2
	midY := n.bounds.Y + n.bounds.H/2
	midZ := n.bounds.Z + n.bounds.D/2
	idx := 0
	if x >= midX {
		idx |= 1
	}
	if y >= midY {
		idx |= 2
	}
	if z >= midZ {
		idx |= 4
	}
	return idx
}

// Tree 八叉树空间索引。零值不可用,用 New 构造。并发安全。
type Tree[ID comparable] struct {
	mu       sync.RWMutex
	root     *node[ID]
	capacity int
	maxDepth int
	index    map[ID]entry[ID]
}

// New 创建八叉树。bounds 定义世界总边界,capacity 为节点分裂阈值(推荐 8~16),
// maxDepth 限制递归深度(推荐 8~12)。
func New[ID comparable](bounds Box, capacity, maxDepth int) *Tree[ID] {
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
func (t *Tree[ID]) Add(id ID, x, y, z float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.index[id]; ok {
		t.removeLocked(old)
	}
	e := entry[ID]{id: id, x: x, y: y, z: z}
	t.index[id] = e
	t.insertLocked(t.root, e, 0)
}

// Move 移动实体到新坐标。不存在则新增。
func (t *Tree[ID]) Move(id ID, x, y, z float64) { t.Add(id, x, y, z) }

// Remove 删除实体。不存在则无操作。
func (t *Tree[ID]) Remove(id ID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.index[id]; ok {
		t.removeLocked(old)
		delete(t.index, id)
	}
}

// Pos 返回实体当前坐标。不存在返回 (0,0,0,false)。
func (t *Tree[ID]) Pos(id ID) (x, y, z float64, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if e, found := t.index[id]; found {
		return e.x, e.y, e.z, true
	}
	return 0, 0, 0, false
}

// Len 返回实体总数。
func (t *Tree[ID]) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.index)
}

// Nearby 返回距 (x,y,z) 球形半径 radius 内的所有实体,按距离升序。
func (t *Tree[ID]) Nearby(x, y, z, radius float64, exclude ...ID) []Entity[ID] {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ex := toSet(exclude)
	var out []Entity[ID]
	r2 := radius * radius
	t.querySphere(t.root, x, y, z, radius, r2, ex, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].Dist < out[j].Dist })
	return out
}

// KNN 返回距 (x,y,z) 最近的 k 个实体。radius 限定搜索范围(<=0 不限)。
func (t *Tree[ID]) KNN(x, y, z float64, k int, radius float64, exclude ...ID) []Entity[ID] {
	if k <= 0 {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	ex := toSet(exclude)
	var out []Entity[ID]
	if radius > 0 {
		r2 := radius * radius
		t.querySphere(t.root, x, y, z, radius, r2, ex, &out)
	} else {
		t.queryAll(t.root, x, y, z, ex, &out)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dist < out[j].Dist })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// QueryBox 返回包围盒内所有实体。
func (t *Tree[ID]) QueryBox(b Box, exclude ...ID) []Entity[ID] {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ex := toSet(exclude)
	var out []Entity[ID]
	t.queryBox(t.root, b, ex, &out)
	return out
}

func (t *Tree[ID]) insertLocked(n *node[ID], e entry[ID], depth int) {
	if !n.bounds.contains(e.x, e.y, e.z) {
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
			ci := n.childFor(o.x, o.y, o.z)
			t.insertLocked(n.children[ci], o, depth+1)
		}
	}
	ci := n.childFor(e.x, e.y, e.z)
	t.insertLocked(n.children[ci], e, depth+1)
}

func (t *Tree[ID]) removeLocked(e entry[ID]) {
	t.removeFromNode(t.root, e)
}

func (t *Tree[ID]) removeFromNode(n *node[ID], e entry[ID]) bool {
	if !n.bounds.contains(e.x, e.y, e.z) {
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
	ci := n.childFor(e.x, e.y, e.z)
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
	n.children = [8]*node[ID]{}
	n.divided = false
}

func (t *Tree[ID]) querySphere(n *node[ID], cx, cy, cz, radius, r2 float64, ex map[ID]struct{}, out *[]Entity[ID]) {
	if !n.bounds.intersectsSphere(cx, cy, cz, radius) {
		return
	}
	if n.isLeaf() {
		for _, e := range n.entries {
			if _, skip := ex[e.id]; skip {
				continue
			}
			dx, dy, dz := e.x-cx, e.y-cy, e.z-cz
			d2 := dx*dx + dy*dy + dz*dz
			if d2 <= r2 {
				*out = append(*out, Entity[ID]{ID: e.id, X: e.x, Y: e.y, Z: e.z, Dist: math.Sqrt(d2)})
			}
		}
		return
	}
	for _, child := range n.children {
		t.querySphere(child, cx, cy, cz, radius, r2, ex, out)
	}
}

func (t *Tree[ID]) queryBox(n *node[ID], b Box, ex map[ID]struct{}, out *[]Entity[ID]) {
	if !n.bounds.intersectsBox(b) {
		return
	}
	if n.isLeaf() {
		for _, e := range n.entries {
			if _, skip := ex[e.id]; skip {
				continue
			}
			if b.contains(e.x, e.y, e.z) {
				*out = append(*out, Entity[ID]{ID: e.id, X: e.x, Y: e.y, Z: e.z})
			}
		}
		return
	}
	for _, child := range n.children {
		t.queryBox(child, b, ex, out)
	}
}

func (t *Tree[ID]) queryAll(n *node[ID], cx, cy, cz float64, ex map[ID]struct{}, out *[]Entity[ID]) {
	if n.isLeaf() {
		for _, e := range n.entries {
			if _, skip := ex[e.id]; skip {
				continue
			}
			dx, dy, dz := e.x-cx, e.y-cy, e.z-cz
			*out = append(*out, Entity[ID]{ID: e.id, X: e.x, Y: e.y, Z: e.z, Dist: math.Sqrt(dx*dx + dy*dy + dz*dz)})
		}
		return
	}
	for _, child := range n.children {
		t.queryAll(child, cx, cy, cz, ex, out)
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
