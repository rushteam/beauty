package cache

import (
	"container/list"
	"hash/maphash"
	"sync"
)

// --- Count-Min 频率草图(4-bit 计数,带老化)---

// cmSketch 用固定内存估计 key 的近似访问频率:depth 行、每行 width 个 4-bit 计数器
// (这里每个计数器用一个 uint8,上限 15)。估计值取各行最小值(Count-Min)。
// 累计增量达到 sampleSize 时"老化":所有计数器减半,让久远的热度自然衰减。
type cmSketch struct {
	rows       [][]uint8
	mask       uint64
	depth      int
	additions  int
	sampleSize int
}

func newCMSketch(width, sampleSize int) *cmSketch {
	w := nextPow2(uint64(width))
	const depth = 4
	rows := make([][]uint8, depth)
	for i := range rows {
		rows[i] = make([]uint8, w)
	}
	return &cmSketch{rows: rows, mask: w - 1, depth: depth, sampleSize: sampleSize}
}

// 用双重散列从一个 64 位哈希派生每行的位置。
func (s *cmSketch) pos(h uint64, i int) uint64 {
	h2 := (h >> 32) | 1 // 保证步长非零
	return (h + uint64(i)*h2) & s.mask
}

func (s *cmSketch) increment(h uint64) {
	for i := 0; i < s.depth; i++ {
		p := s.pos(h, i)
		if s.rows[i][p] < 15 {
			s.rows[i][p]++
		}
	}
	s.additions++
	if s.additions >= s.sampleSize {
		s.reset()
	}
}

func (s *cmSketch) estimate(h uint64) uint8 {
	min := uint8(15)
	for i := 0; i < s.depth; i++ {
		if v := s.rows[i][s.pos(h, i)]; v < min {
			min = v
		}
	}
	return min
}

// reset 老化:所有计数器减半,累计数减半。
func (s *cmSketch) reset() {
	for i := range s.rows {
		row := s.rows[i]
		for j := range row {
			row[j] >>= 1
		}
	}
	s.additions >>= 1
}

func nextPow2(n uint64) uint64 {
	if n < 1 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	return n + 1
}

// --- W-TinyLFU ---

type segKind uint8

const (
	segWindow segKind = iota
	segProbation
	segProtected
)

type tlItem[K comparable, V any] struct {
	key K
	val V
	seg segKind
}

// TinyLFU 是 W-TinyLFU 缓存:小窗口 LRU(承接新写入的突发)+ 主 SLRU(probation/protected 两段)。
// 当窗口淘汰一个候选者时,用 Count-Min 频率与主区的淘汰victim 比较,频率更高才准入——于是
// 一次性涌入的大量冷 key(扫描)无法挤掉真正的热点。命中率通常显著高于纯 LRU,内存仅多一份草图。
type TinyLFU[K comparable, V any] struct {
	mu    sync.Mutex
	items map[K]*list.Element

	window    *list.List
	probation *list.List
	protected *list.List

	windowCap    int
	mainCap      int
	protectedCap int

	sketch *cmSketch
	seed   maphash.Seed
}

// NewTinyLFU 创建容量为 capacity 的 W-TinyLFU(capacity<=0 视为 1)。
func NewTinyLFU[K comparable, V any](capacity int) *TinyLFU[K, V] {
	if capacity <= 0 {
		capacity = 1
	}
	windowCap := capacity / 100
	if windowCap < 1 {
		windowCap = 1
	}
	mainCap := capacity - windowCap
	protectedCap := mainCap * 80 / 100
	sampleSize := 10 * capacity
	if sampleSize < 100 {
		sampleSize = 100
	}
	return &TinyLFU[K, V]{
		items:        make(map[K]*list.Element, capacity),
		window:       list.New(),
		probation:    list.New(),
		protected:    list.New(),
		windowCap:    windowCap,
		mainCap:      mainCap,
		protectedCap: protectedCap,
		sketch:       newCMSketch(capacity, sampleSize),
		seed:         maphash.MakeSeed(),
	}
}

func (c *TinyLFU[K, V]) hash(key K) uint64 { return maphash.Comparable(c.seed, key) }

func (c *TinyLFU[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.sketch.increment(c.hash(key))
	c.touch(el)
	return el.Value.(*tlItem[K, V]).val, true
}

func (c *TinyLFU[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sketch.increment(c.hash(key))
	if el, ok := c.items[key]; ok {
		el.Value.(*tlItem[K, V]).val = value
		c.touch(el)
		return
	}
	// 新键先进窗口。
	it := &tlItem[K, V]{key: key, val: value, seg: segWindow}
	el := c.window.PushFront(it)
	c.items[key] = el
	if c.window.Len() > c.windowCap {
		c.admitFromWindow()
	}
}

func (c *TinyLFU[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.remove(el)
	}
}

func (c *TinyLFU[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.window.Len() + c.probation.Len() + c.protected.Len()
}

// touch 处理一次访问的段内移动/晋升(不改总量)。
func (c *TinyLFU[K, V]) touch(el *list.Element) {
	it := el.Value.(*tlItem[K, V])
	switch it.seg {
	case segWindow:
		c.window.MoveToFront(el)
	case segProtected:
		c.protected.MoveToFront(el)
	case segProbation:
		// probation 命中 → 晋升 protected;若 protected 超额,降级其 LRU 回 probation。
		c.probation.Remove(el)
		it.seg = segProtected
		nel := c.protected.PushFront(it)
		c.items[it.key] = nel
		if c.protected.Len() > c.protectedCap {
			if back := c.protected.Back(); back != nil {
				bit := back.Value.(*tlItem[K, V])
				c.protected.Remove(back)
				bit.seg = segProbation
				c.items[bit.key] = c.probation.PushFront(bit)
			}
		}
	}
}

// admitFromWindow 把窗口 LRU 候选者尝试准入主区:主区有空位直接进 probation;
// 否则与 probation 的 LRU victim 比频率,候选者更高才替换,否则丢弃候选者。
func (c *TinyLFU[K, V]) admitFromWindow() {
	candEl := c.window.Back()
	if candEl == nil {
		return
	}
	cand := candEl.Value.(*tlItem[K, V])
	c.window.Remove(candEl)

	if c.mainCap <= 0 { // 无主区(极小容量):直接丢弃候选者
		delete(c.items, cand.key)
		return
	}
	if c.probation.Len()+c.protected.Len() < c.mainCap { // 主区有空位
		cand.seg = segProbation
		c.items[cand.key] = c.probation.PushFront(cand)
		return
	}
	// 主区已满:取 probation 的 LRU 作 victim(空则取 protected 的 LRU)。
	victimEl := c.probation.Back()
	if victimEl == nil {
		victimEl = c.protected.Back()
	}
	victim := victimEl.Value.(*tlItem[K, V])
	if c.sketch.estimate(c.hash(cand.key)) > c.sketch.estimate(c.hash(victim.key)) {
		c.remove(victimEl)
		cand.seg = segProbation
		c.items[cand.key] = c.probation.PushFront(cand)
	} else {
		delete(c.items, cand.key) // 候选者频率不敌 victim,丢弃
	}
}

func (c *TinyLFU[K, V]) remove(el *list.Element) {
	it := el.Value.(*tlItem[K, V])
	switch it.seg {
	case segWindow:
		c.window.Remove(el)
	case segProbation:
		c.probation.Remove(el)
	case segProtected:
		c.protected.Remove(el)
	}
	delete(c.items, it.key)
}

var _ Cache[string, int] = (*TinyLFU[string, int])(nil)
