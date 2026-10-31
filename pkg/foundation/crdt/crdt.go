// Package crdt 提供「无冲突复制数据类型」(CRDT, Conflict-free Replicated Data Type)的几个
// 核心成员。CRDT 让多个副本各自独立更新、乱序交换状态后仍能收敛到同一结果,无需中心协调、
// 无需加锁等待——这是多端协同编辑、离线优先同步(offline-first)、多活/弱网下「最终一致」的
// 数学基石。与 diff 的三路合并(需共同祖先、可能产生冲突)不同,CRDT 的 Merge 天然满足
// 交换律/结合律/幂等,永不冲突。
//
// 本包实现的是**状态型 CRDT(CvRDT)**:每个副本持有完整状态,同步时交换状态并 Merge。
// 成员:
//   - GCounter:只增计数器(多副本并发自增,如全局 PV);
//   - PNCounter:可增可减计数器(点赞/库存,基于两个 GCounter);
//   - GSet:只增集合(并集合并);
//   - ORSet:可增可删集合(Observed-Remove,支持删除且并发 add 优先,协同场景常用);
//   - LWWRegister:最后写入胜寄存器(按时间戳 + 副本号定序的单值)。
//
// 副本标识 nodeID 必须全局唯一(每个副本一个,用于计数器分量与 ORSet 唯一标签)。
// 并发不安全:同一副本的本地更新应由调用方串行化;Merge 也需在外层协调。纯标准库。
package crdt

import "strconv"

// ===== GCounter:只增计数器 =====

// GCounter 是只增计数器:每个副本维护自己的分量,总值为各分量之和;Merge 取各分量的较大值。
type GCounter struct {
	counts map[string]uint64
	delta  map[string]uint64 // δ-CRDT:自上次 Delta() 以来发生变化的分量(增量同步缓冲)
}

// NewGCounter 创建空的只增计数器。
func NewGCounter() *GCounter { return &GCounter{counts: map[string]uint64{}} }

// Inc 让 node 副本的分量增加 n。
func (c *GCounter) Inc(node string, n uint64) {
	c.counts[node] += n
	c.recordDelta(node, c.counts[node])
}

// Value 返回当前总值(各副本分量之和)。
func (c *GCounter) Value() uint64 {
	var sum uint64
	for _, v := range c.counts {
		sum += v
	}
	return sum
}

// Merge 并入另一副本状态:每个分量取两者较大值(幂等、可交换)。
func (c *GCounter) Merge(o *GCounter) {
	for node, v := range o.counts {
		if v > c.counts[node] {
			c.counts[node] = v
		}
	}
}

// Clone 深拷贝。
func (c *GCounter) Clone() *GCounter {
	nc := NewGCounter()
	for k, v := range c.counts {
		nc.counts[k] = v
	}
	return nc
}

// ===== PNCounter:可增可减计数器 =====

// PNCounter 用两个 GCounter(正分量 p、负分量 n)实现可增可减;值 = p - n。
type PNCounter struct {
	p, n *GCounter
}

// NewPNCounter 创建可增可减计数器。
func NewPNCounter() *PNCounter { return &PNCounter{p: NewGCounter(), n: NewGCounter()} }

// Inc 让 node 副本增加 delta。
func (c *PNCounter) Inc(node string, delta uint64) { c.p.Inc(node, delta) }

// Dec 让 node 副本减少 delta。
func (c *PNCounter) Dec(node string, delta uint64) { c.n.Inc(node, delta) }

// Value 返回当前值(可为负)。
func (c *PNCounter) Value() int64 { return int64(c.p.Value()) - int64(c.n.Value()) }

// Merge 并入另一副本。
func (c *PNCounter) Merge(o *PNCounter) {
	c.p.Merge(o.p)
	c.n.Merge(o.n)
}

// Clone 深拷贝。
func (c *PNCounter) Clone() *PNCounter {
	return &PNCounter{p: c.p.Clone(), n: c.n.Clone()}
}

// ===== GSet:只增集合 =====

// GSet 是只增集合:只能 Add,Merge 为并集。
type GSet[T comparable] struct {
	m     map[T]struct{}
	delta map[T]struct{} // δ-CRDT:自上次 Delta() 以来新增的元素
}

// NewGSet 创建空只增集合。
func NewGSet[T comparable]() *GSet[T] { return &GSet[T]{m: map[T]struct{}{}} }

// Add 加入元素。
func (s *GSet[T]) Add(e T) {
	if _, ok := s.m[e]; ok {
		return
	}
	s.m[e] = struct{}{}
	s.recordDelta(e)
}

// Contains 判断是否包含。
func (s *GSet[T]) Contains(e T) bool { _, ok := s.m[e]; return ok }

// Len 返回元素个数。
func (s *GSet[T]) Len() int { return len(s.m) }

// Elements 返回全部元素(无序)。
func (s *GSet[T]) Elements() []T {
	out := make([]T, 0, len(s.m))
	for e := range s.m {
		out = append(out, e)
	}
	return out
}

// Merge 并入另一副本(并集)。
func (s *GSet[T]) Merge(o *GSet[T]) {
	for e := range o.m {
		s.m[e] = struct{}{}
	}
}

// Clone 深拷贝。
func (s *GSet[T]) Clone() *GSet[T] {
	ns := NewGSet[T]()
	for e := range s.m {
		ns.m[e] = struct{}{}
	}
	return ns
}

// ===== ORSet:可增可删集合(Observed-Remove) =====

// ORSet 是「观察-删除」集合:支持 Add 与 Remove,并发的 add 与 remove 以 add 优先(重新加入
// 的元素不会被旧的删除误删)。每次 Add 生成一个唯一标签(dot),Remove 只墓碑当前**已观察到**
// 的标签;某元素存在 ⟺ 它有一个未被墓碑的标签。Merge 为「加标签并集 + 墓碑并集」。
type ORSet[T comparable] struct {
	node string
	seq  uint64
	adds map[T]map[string]struct{} // 元素 -> 其标签集合
	tomb map[string]struct{}       // 已删除(墓碑)的标签集合
	// δ-CRDT:自上次 Delta() 以来新增的标签与墓碑(增量同步缓冲)。
	deltaAdds map[T]map[string]struct{}
	deltaTomb map[string]struct{}
}

// NewORSet 创建 ORSet;node 为本副本唯一标识(用于生成唯一标签)。
func NewORSet[T comparable](node string) *ORSet[T] {
	return &ORSet[T]{
		node: node,
		adds: map[T]map[string]struct{}{},
		tomb: map[string]struct{}{},
	}
}

// Add 加入元素(生成一个新的唯一标签)。
func (s *ORSet[T]) Add(e T) {
	s.seq++
	tag := s.node + "#" + strconv.FormatUint(s.seq, 10)
	tags := s.adds[e]
	if tags == nil {
		tags = map[string]struct{}{}
		s.adds[e] = tags
	}
	tags[tag] = struct{}{}
	s.recordDeltaAdd(e, tag)
}

// Remove 删除元素:把它当前所有已观察标签打上墓碑。未包含则无操作。
func (s *ORSet[T]) Remove(e T) {
	for tag := range s.adds[e] {
		s.tomb[tag] = struct{}{}
		s.recordDeltaTomb(tag)
	}
}

// Contains 判断元素是否存在(存在未被墓碑的标签)。
func (s *ORSet[T]) Contains(e T) bool {
	for tag := range s.adds[e] {
		if _, dead := s.tomb[tag]; !dead {
			return true
		}
	}
	return false
}

// Elements 返回当前存在的全部元素(无序)。
func (s *ORSet[T]) Elements() []T {
	var out []T
	for e := range s.adds {
		if s.Contains(e) {
			out = append(out, e)
		}
	}
	return out
}

// Merge 并入另一副本:标签并集 + 墓碑并集。
func (s *ORSet[T]) Merge(o *ORSet[T]) {
	for e, tags := range o.adds {
		cur := s.adds[e]
		if cur == nil {
			cur = map[string]struct{}{}
			s.adds[e] = cur
		}
		for tag := range tags {
			cur[tag] = struct{}{}
		}
	}
	for tag := range o.tomb {
		s.tomb[tag] = struct{}{}
	}
}

// Clone 深拷贝(seq 归零,后续本地 Add 继续生成唯一标签依赖 node 唯一性)。
func (s *ORSet[T]) Clone() *ORSet[T] {
	ns := NewORSet[T](s.node)
	ns.seq = s.seq
	for e, tags := range s.adds {
		m := make(map[string]struct{}, len(tags))
		for t := range tags {
			m[t] = struct{}{}
		}
		ns.adds[e] = m
	}
	for t := range s.tomb {
		ns.tomb[t] = struct{}{}
	}
	return ns
}

// ===== LWWRegister:最后写入胜寄存器 =====

// LWWRegister 是最后写入胜(Last-Write-Wins)寄存器:并发写入按 (时间戳, 副本号) 定序,
// 较大者胜。时间戳由调用方提供(物理时钟 UnixNano 或逻辑时钟均可);时间戳相同用副本号
// (字典序)打破平局,保证所有副本收敛到同一值。
type LWWRegister[T any] struct {
	val   T
	ts    int64
	node  string
	set   bool
	dirty bool // δ-CRDT:自上次 Delta() 以来当前值是否发生过变化(增量同步标记)
}

// NewLWWRegister 创建空寄存器。
func NewLWWRegister[T any]() *LWWRegister[T] { return &LWWRegister[T]{} }

// Set 写入值(时间戳 ts、副本 node)。仅当 (ts,node) 严格大于当前时才生效。
func (r *LWWRegister[T]) Set(val T, ts int64, node string) {
	if r.newer(ts, node) {
		r.val, r.ts, r.node, r.set, r.dirty = val, ts, node, true, true
	}
}

// Get 返回当前值与是否已被写入过。
func (r *LWWRegister[T]) Get() (T, bool) { return r.val, r.set }

// Merge 并入另一副本(取较新者)。
func (r *LWWRegister[T]) Merge(o *LWWRegister[T]) {
	if o.set && r.newer(o.ts, o.node) {
		r.val, r.ts, r.node, r.set = o.val, o.ts, o.node, true
	}
}

// newer 判断 (ts,node) 是否比当前记录更新(未写入过则恒为真)。
func (r *LWWRegister[T]) newer(ts int64, node string) bool {
	if !r.set {
		return true
	}
	if ts != r.ts {
		return ts > r.ts
	}
	return node > r.node
}

// Clone 浅拷贝(值按 T 的赋值语义);克隆是全新副本,delta 标记归零。
func (r *LWWRegister[T]) Clone() *LWWRegister[T] {
	nr := *r
	nr.dirty = false
	return &nr
}
