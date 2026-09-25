// Package skiplist 提供泛型有序映射的跳表(Skip List)实现:一个多层链表,靠概率化的
// "高层快速道"把有序查找/插入/删除做到期望 O(log n),实现远比平衡树简单,且天然支持
// 范围遍历。
//
// 适用场景:
//   - 内存有序索引 / 排行榜(按分数排序,取 Top-K、范围查询、名次);Redis ZSET 的核心
//     就是跳表;
//   - 需要"按 key 有序遍历 + 高效点查/插删"的轻量 KV(比红黑树好写、好并发化);
//   - 定时器 / 延迟队列的有序结构(按到期时间排序)。
//
// 与相邻结构的取舍:
//   - map 无序、点查 O(1),但不支持范围/名次查询;
//   - 本跳表有序,点查/插删期望 O(log n),额外支持 Rank / 范围遍历 / 前驱后继。
//
// 泛型:K 为键类型,由构造时传入的 cmp(比较函数)定序(cmp(a,b)<0 表示 a<b),
// 因此可对任意类型(含结构体、复合键)排序,不要求 K 可比较为 constraints.Ordered。
// V 为任意值类型。键唯一:Set 同键覆盖值。
//
// 并发安全:非并发安全(读写需上层加锁)。零值不可用,用 New 构造。
package skiplist

import "math/rand"

const (
	maxLevel = 32   // 最大层数,支持约 4^32 元素绰绰有余
	pFactor  = 0.25 // 每升一层的概率(0.25 兼顾查找速度与空间)
)

// node 跳表节点。next[i] 指向第 i 层的后继;span[i] 为该跨度覆盖的底层节点数(用于 Rank)。
type node[K, V any] struct {
	key   K
	value V
	next  []*node[K, V]
	span  []int
}

// SkipList 是按 cmp 定序的有序映射。零值不可用,用 New 构造。非并发安全。
type SkipList[K, V any] struct {
	head   *node[K, V]
	level  int // 当前最高层(1-based)
	length int
	cmp    func(a, b K) int
	rng    *rand.Rand
}

// New 创建跳表。cmp 为键比较函数:a<b 返回负,a==b 返回 0,a>b 返回正。cmp 为 nil 会 panic。
func New[K, V any](cmp func(a, b K) int) *SkipList[K, V] {
	if cmp == nil {
		panic("skiplist: cmp must not be nil")
	}
	return &SkipList[K, V]{
		head:  &node[K, V]{next: make([]*node[K, V], maxLevel), span: make([]int, maxLevel)},
		level: 1,
		cmp:   cmp,
		rng:   rand.New(rand.NewSource(1)), // 固定种子:确定性,测试可复现
	}
}

// randomLevel 按 pFactor 概率决定新节点层数。
func (s *SkipList[K, V]) randomLevel() int {
	lvl := 1
	for lvl < maxLevel && s.rng.Float64() < pFactor {
		lvl++
	}
	return lvl
}

// Len 返回元素个数。
func (s *SkipList[K, V]) Len() int { return s.length }

// Set 插入或更新键 key 的值。返回该键此前是否已存在。
func (s *SkipList[K, V]) Set(key K, value V) (existed bool) {
	update := make([]*node[K, V], maxLevel) // 每层待更新的前驱
	rank := make([]int, maxLevel)           // 每层前驱的累计名次(用于维护 span)
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		if i == s.level-1 {
			rank[i] = 0
		} else {
			rank[i] = rank[i+1]
		}
		for x.next[i] != nil && s.cmp(x.next[i].key, key) < 0 {
			rank[i] += x.span[i]
			x = x.next[i]
		}
		update[i] = x
	}
	// 命中已存在的键:更新值
	if nxt := update[0].next[0]; nxt != nil && s.cmp(nxt.key, key) == 0 {
		nxt.value = value
		return true
	}

	lvl := s.randomLevel()
	if lvl > s.level {
		for i := s.level; i < lvl; i++ {
			rank[i] = 0
			update[i] = s.head
			update[i].span[i] = s.length
		}
		s.level = lvl
	}

	n := &node[K, V]{key: key, value: value, next: make([]*node[K, V], lvl), span: make([]int, lvl)}
	for i := 0; i < lvl; i++ {
		n.next[i] = update[i].next[i]
		update[i].next[i] = n
		// 维护 span:插入点在 rank[0] 处
		n.span[i] = update[i].span[i] - (rank[0] - rank[i])
		update[i].span[i] = (rank[0] - rank[i]) + 1
	}
	// 高于新节点层数的前驱,其 span 各 +1(底层多了一个节点)
	for i := lvl; i < s.level; i++ {
		update[i].span[i]++
	}
	s.length++
	return false
}

// Get 返回键 key 的值与是否存在。
func (s *SkipList[K, V]) Get(key K) (V, bool) {
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil && s.cmp(x.next[i].key, key) < 0 {
			x = x.next[i]
		}
	}
	x = x.next[0]
	if x != nil && s.cmp(x.key, key) == 0 {
		return x.value, true
	}
	var zero V
	return zero, false
}

// Contains 判断键是否存在。
func (s *SkipList[K, V]) Contains(key K) bool {
	_, ok := s.Get(key)
	return ok
}

// Delete 删除键 key,返回是否删除成功(键此前存在)。
func (s *SkipList[K, V]) Delete(key K) bool {
	update := make([]*node[K, V], maxLevel)
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil && s.cmp(x.next[i].key, key) < 0 {
			x = x.next[i]
		}
		update[i] = x
	}
	target := x.next[0]
	if target == nil || s.cmp(target.key, key) != 0 {
		return false
	}
	for i := 0; i < s.level; i++ {
		if update[i].next[i] == target {
			update[i].span[i] += target.span[i] - 1
			update[i].next[i] = target.next[i]
		} else {
			update[i].span[i]--
		}
	}
	// 回收空出的高层
	for s.level > 1 && s.head.next[s.level-1] == nil {
		s.level--
	}
	s.length--
	return true
}

// Rank 返回键 key 的名次(从 1 开始,即比它小的元素个数 +1);不存在返回 0。
func (s *SkipList[K, V]) Rank(key K) int {
	x := s.head
	rank := 0
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil && s.cmp(x.next[i].key, key) <= 0 {
			rank += x.span[i]
			x = x.next[i]
		}
	}
	if x != s.head && s.cmp(x.key, key) == 0 {
		return rank
	}
	return 0
}

// ByRank 返回名次为 r(1-based)的键值;越界返回零值与 false。
func (s *SkipList[K, V]) ByRank(r int) (K, V, bool) {
	var zk K
	var zv V
	if r <= 0 || r > s.length {
		return zk, zv, false
	}
	x := s.head
	traversed := 0
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil && traversed+x.span[i] <= r {
			traversed += x.span[i]
			x = x.next[i]
		}
		if traversed == r {
			return x.key, x.value, true
		}
	}
	return zk, zv, false
}

// Min 返回最小键的键值;空表返回 false。
func (s *SkipList[K, V]) Min() (K, V, bool) {
	x := s.head.next[0]
	if x == nil {
		var zk K
		var zv V
		return zk, zv, false
	}
	return x.key, x.value, true
}

// Max 返回最大键的键值;空表返回 false。
func (s *SkipList[K, V]) Max() (K, V, bool) {
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil {
			x = x.next[i]
		}
	}
	if x == s.head {
		var zk K
		var zv V
		return zk, zv, false
	}
	return x.key, x.value, true
}

// Range 按升序遍历所有键值,fn 返回 false 提前停止。
func (s *SkipList[K, V]) Range(fn func(key K, value V) bool) {
	for x := s.head.next[0]; x != nil; x = x.next[0] {
		if !fn(x.key, x.value) {
			return
		}
	}
}

// RangeFrom 从第一个 >= start 的键开始按升序遍历,fn 返回 false 提前停止。
// 适合范围查询(如"分数在 [start, ...) 的排行榜段")。
func (s *SkipList[K, V]) RangeFrom(start K, fn func(key K, value V) bool) {
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil && s.cmp(x.next[i].key, start) < 0 {
			x = x.next[i]
		}
	}
	for x = x.next[0]; x != nil; x = x.next[0] {
		if !fn(x.key, x.value) {
			return
		}
	}
}
