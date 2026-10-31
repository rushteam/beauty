// Package intervaltree 提供区间树(Interval Tree):管理一批区间 [Low, High],高效回答
// 「哪些区间覆盖了某个点」(Stab)与「哪些区间与给定区间相交」(Overlap)。这是时间区间
// 冲突检测的标准结构——会议室/资源预订、排期占用、价格有效期、IP 段归属、基因组区间等。
//
// 朴素做法是遍历所有区间逐个判相交 O(n);区间树借助「按左端点组织的 BST + 子树内最大右端点
// (maxHigh)增广」把查询降到 O(log n + k)(k 为命中数):用 maxHigh 剪掉整棵不可能相交的子树。
//
// 平衡用 treap(随机优先级的 BST),实现简单、期望平衡 O(log n),无需手写红黑树旋转配色。
// 坐标用 int64(时间戳、序号、端口、偏移……皆可),区间为**闭区间** [Low, High]:当且仅当
// a.Low<=b.High && b.Low<=a.High 判为相交。泛型 V 为每个区间携带的载荷。
//
// 并发不安全(与本仓其它数据结构一致);零值不可用,用 New 构造。
package intervaltree

import (
	"math"
	"math/rand/v2"
)

// Interval 是一个闭区间 [Low, High](要求 Low<=High)。
type Interval struct {
	Low, High int64
}

// Overlaps 判断两区间是否相交(闭区间)。
func (iv Interval) Overlaps(o Interval) bool {
	return iv.Low <= o.High && o.Low <= iv.High
}

// Contains 判断区间是否覆盖点 p。
func (iv Interval) Contains(p int64) bool { return iv.Low <= p && p <= iv.High }

// Match 是一次查询命中:区间及其载荷。
type Match[V any] struct {
	Interval
	Value V
}

type node[V any] struct {
	iv          Interval
	val         V
	max         int64 // 子树内最大 High
	prio        uint64
	left, right *node[V]
}

func nmax[V any](n *node[V]) int64 {
	if n == nil {
		return math.MinInt64
	}
	return n.max
}

func (n *node[V]) update() {
	n.max = n.iv.High
	if m := nmax(n.left); m > n.max {
		n.max = m
	}
	if m := nmax(n.right); m > n.max {
		n.max = m
	}
}

// Tree 是区间树。
type Tree[V any] struct {
	root *node[V]
	size int
}

// New 创建空区间树。
func New[V any]() *Tree[V] { return &Tree[V]{} }

// Len 返回区间个数。
func (t *Tree[V]) Len() int { return t.size }

// Insert 加入区间 [low, high](允许重复区间);low>high 时自动交换。
func (t *Tree[V]) Insert(low, high int64, v V) {
	if low > high {
		low, high = high, low
	}
	t.root = insert(t.root, &node[V]{iv: Interval{low, high}, val: v, prio: rand.Uint64()})
	t.size++
}

func insert[V any](n, x *node[V]) *node[V] {
	if n == nil {
		x.update()
		return x
	}
	// BST 键:先按 Low,再按 High。
	if less(x.iv, n.iv) {
		n.left = insert(n.left, x)
		if n.left.prio > n.prio {
			n = rotateRight(n)
		}
	} else {
		n.right = insert(n.right, x)
		if n.right.prio > n.prio {
			n = rotateLeft(n)
		}
	}
	n.update()
	return n
}

func less(a, b Interval) bool {
	if a.Low != b.Low {
		return a.Low < b.Low
	}
	return a.High < b.High
}

func rotateRight[V any](n *node[V]) *node[V] {
	l := n.left
	n.left = l.right
	l.right = n
	n.update()
	l.update()
	return l
}

func rotateLeft[V any](n *node[V]) *node[V] {
	r := n.right
	n.right = r.left
	r.left = n
	n.update()
	r.update()
	return r
}

// Remove 删除一个与 [low, high] 端点完全相等的区间,返回是否删到。重复区间只删一个。
func (t *Tree[V]) Remove(low, high int64) bool {
	if low > high {
		low, high = high, low
	}
	var ok bool
	t.root, ok = remove(t.root, Interval{low, high})
	if ok {
		t.size--
	}
	return ok
}

func remove[V any](n *node[V], iv Interval) (*node[V], bool) {
	if n == nil {
		return nil, false
	}
	var ok bool
	switch {
	case less(iv, n.iv):
		n.left, ok = remove(n.left, iv)
	case less(n.iv, iv):
		n.right, ok = remove(n.right, iv)
	default: // 命中
		return deleteRoot(n), true
	}
	if n != nil {
		n.update()
	}
	return n, ok
}

// deleteRoot 把 n 旋到叶子后摘除。
func deleteRoot[V any](n *node[V]) *node[V] {
	if n.left == nil {
		return n.right
	}
	if n.right == nil {
		return n.left
	}
	if n.left.prio > n.right.prio {
		n = rotateRight(n)
		n.right = deleteRoot(n.right)
	} else {
		n = rotateLeft(n)
		n.left = deleteRoot(n.left)
	}
	n.update()
	return n
}

// Stab 返回所有覆盖点 p 的区间(无序)。
func (t *Tree[V]) Stab(p int64) []Match[V] {
	var out []Match[V]
	stab(t.root, p, &out)
	return out
}

func stab[V any](n *node[V], p int64, out *[]Match[V]) {
	if n == nil || p > n.max {
		return // 整棵子树的 High 都 < p,不可能覆盖
	}
	stab(n.left, p, out)
	if n.iv.Contains(p) {
		*out = append(*out, Match[V]{n.iv, n.val})
	}
	if p >= n.iv.Low { // 右子树的 Low 都 >= n.Low;若 p<n.Low 则右子树也 Low>p 无覆盖
		stab(n.right, p, out)
	}
}

// Overlap 返回所有与 [low, high] 相交的区间(无序)。
func (t *Tree[V]) Overlap(low, high int64) []Match[V] {
	if low > high {
		low, high = high, low
	}
	q := Interval{low, high}
	var out []Match[V]
	overlap(t.root, q, &out)
	return out
}

func overlap[V any](n *node[V], q Interval, out *[]Match[V]) {
	if n == nil || q.Low > n.max {
		return // 子树最大 High 都 < 查询下界,无相交
	}
	overlap(n.left, q, out)
	if n.iv.Overlaps(q) {
		*out = append(*out, Match[V]{n.iv, n.val})
	}
	if n.iv.Low <= q.High { // 右子树 Low>=n.Low;若 n.Low>q.High 则右子树全部 Low>q.High
		overlap(n.right, q, out)
	}
}

// Any 判断是否存在与 [low, high] 相交的区间(短路,比 Overlap 省分配)。
func (t *Tree[V]) Any(low, high int64) bool {
	if low > high {
		low, high = high, low
	}
	return anyOverlap(t.root, Interval{low, high})
}

func anyOverlap[V any](n *node[V], q Interval) bool {
	if n == nil || q.Low > n.max {
		return false
	}
	if anyOverlap(n.left, q) {
		return true
	}
	if n.iv.Overlaps(q) {
		return true
	}
	if n.iv.Low <= q.High {
		return anyOverlap(n.right, q)
	}
	return false
}
