package crdt

// 本文件实现**δ-CRDT(delta-state CRDT)的增量同步**。
//
// 状态型 CRDT(CvRDT)最朴素的反熵是「每次把整份状态发给对端再 Merge」:正确但随状态增长
// 带宽浪费严重。δ-CRDT 的改进:每个副本在本地变更(Inc/Add/Remove/Set)时顺手把「这次新增
// 的那一小片状态」累积到一个 delta 缓冲;同步时只发 `Delta()`(自上次以来的增量),对端用
// `MergeDelta` 并入即可。delta 本身仍是同类型的合法(偏)状态,Merge 依旧满足幂等/交换/结合,
// 所以增量可乱序、可重复投递而不破坏收敛——与「全量 Merge」结果一致,但带宽远小。
//
// 用法(gossip/反熵循环):
//
//	d := local.Delta()          // 取出并清空本地增量
//	if d.HasDelta() 的等价判断... 一般直接发送序列化后的 d
//	// 对端:
//	remote.MergeDelta(d)        // 并入,且继续向更远的副本传播
//
// `MergeDelta` 既并入状态,又把收到的增量累积进本地 delta 缓冲,从而让增量沿 gossip 拓扑
// 逐跳传播(传递性)。需要周期性做一次全量 `Merge` 兜底,以消除丢包/新副本加入带来的空洞。

// ===== GCounter =====

func (c *GCounter) recordDelta(node string, v uint64) {
	if c.delta == nil {
		c.delta = map[string]uint64{}
	}
	if v > c.delta[node] {
		c.delta[node] = v
	}
}

// HasDelta 报告是否有待发送的增量。
func (c *GCounter) HasDelta() bool { return len(c.delta) > 0 }

// Delta 取出自上次调用以来累积的增量(同类型的偏状态),并清空缓冲。
func (c *GCounter) Delta() *GCounter {
	d := NewGCounter()
	for k, v := range c.delta {
		d.counts[k] = v
	}
	c.delta = nil
	return d
}

// MergeDelta 并入对端发来的增量,并把新抬升的分量累积进本地 delta 以便继续传播。
func (c *GCounter) MergeDelta(o *GCounter) {
	for node, v := range o.counts {
		if v > c.counts[node] {
			c.counts[node] = v
			c.recordDelta(node, v)
		}
	}
}

// ===== PNCounter(委托给内部两个 GCounter) =====

// HasDelta 报告是否有待发送的增量。
func (c *PNCounter) HasDelta() bool { return c.p.HasDelta() || c.n.HasDelta() }

// Delta 取出增量并清空缓冲。
func (c *PNCounter) Delta() *PNCounter {
	return &PNCounter{p: c.p.Delta(), n: c.n.Delta()}
}

// MergeDelta 并入对端增量。
func (c *PNCounter) MergeDelta(o *PNCounter) {
	c.p.MergeDelta(o.p)
	c.n.MergeDelta(o.n)
}

// ===== GSet =====

func (s *GSet[T]) recordDelta(e T) {
	if s.delta == nil {
		s.delta = map[T]struct{}{}
	}
	s.delta[e] = struct{}{}
}

// HasDelta 报告是否有待发送的增量。
func (s *GSet[T]) HasDelta() bool { return len(s.delta) > 0 }

// Delta 取出增量并清空缓冲。
func (s *GSet[T]) Delta() *GSet[T] {
	d := NewGSet[T]()
	for e := range s.delta {
		d.m[e] = struct{}{}
	}
	s.delta = nil
	return d
}

// MergeDelta 并入对端增量,新增元素累积进本地 delta 以便继续传播。
func (s *GSet[T]) MergeDelta(o *GSet[T]) {
	for e := range o.m {
		if _, ok := s.m[e]; !ok {
			s.m[e] = struct{}{}
			s.recordDelta(e)
		}
	}
}

// ===== ORSet =====

func (s *ORSet[T]) recordDeltaAdd(e T, tag string) {
	if s.deltaAdds == nil {
		s.deltaAdds = map[T]map[string]struct{}{}
	}
	m := s.deltaAdds[e]
	if m == nil {
		m = map[string]struct{}{}
		s.deltaAdds[e] = m
	}
	m[tag] = struct{}{}
}

func (s *ORSet[T]) recordDeltaTomb(tag string) {
	if s.deltaTomb == nil {
		s.deltaTomb = map[string]struct{}{}
	}
	s.deltaTomb[tag] = struct{}{}
}

// HasDelta 报告是否有待发送的增量。
func (s *ORSet[T]) HasDelta() bool { return len(s.deltaAdds) > 0 || len(s.deltaTomb) > 0 }

// Delta 取出增量(携带新增标签与新增墓碑的偏状态)并清空缓冲。
func (s *ORSet[T]) Delta() *ORSet[T] {
	d := NewORSet[T](s.node)
	for e, tags := range s.deltaAdds {
		m := make(map[string]struct{}, len(tags))
		for t := range tags {
			m[t] = struct{}{}
		}
		d.adds[e] = m
	}
	for t := range s.deltaTomb {
		d.tomb[t] = struct{}{}
	}
	s.deltaAdds, s.deltaTomb = nil, nil
	return d
}

// MergeDelta 并入对端增量(标签并集 + 墓碑并集),并把新增部分累积进本地 delta 以便继续传播。
func (s *ORSet[T]) MergeDelta(o *ORSet[T]) {
	for e, tags := range o.adds {
		cur := s.adds[e]
		if cur == nil {
			cur = map[string]struct{}{}
			s.adds[e] = cur
		}
		for tag := range tags {
			if _, ok := cur[tag]; !ok {
				cur[tag] = struct{}{}
				s.recordDeltaAdd(e, tag)
			}
		}
	}
	for tag := range o.tomb {
		if _, ok := s.tomb[tag]; !ok {
			s.tomb[tag] = struct{}{}
			s.recordDeltaTomb(tag)
		}
	}
}

// ===== LWWRegister =====

// HasDelta 报告是否有待发送的增量。
func (r *LWWRegister[T]) HasDelta() bool { return r.dirty }

// Delta 取出增量:若自上次以来当前值变化过,返回携带当前胜出值的寄存器;否则返回空寄存器。清空标记。
func (r *LWWRegister[T]) Delta() *LWWRegister[T] {
	d := NewLWWRegister[T]()
	if r.dirty {
		d.val, d.ts, d.node, d.set = r.val, r.ts, r.node, r.set
	}
	r.dirty = false
	return d
}

// MergeDelta 并入对端增量(按时间戳取较新者);若本地值因此改变则置 dirty 以便继续传播。
func (r *LWWRegister[T]) MergeDelta(o *LWWRegister[T]) {
	if o.set && r.newer(o.ts, o.node) {
		r.val, r.ts, r.node, r.set, r.dirty = o.val, o.ts, o.node, true, true
	}
}
