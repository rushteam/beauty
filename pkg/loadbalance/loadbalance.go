// Package loadbalance 提供通用的负载均衡算法原语,不绑定任何 RPC/服务发现框架。
//
// 三个算法:
//   - ConsistentHash:一致性哈希(虚拟节点 + maphash),按 key 路由到稳定节点,
//     支持权重与副本,适合"会话粘性 / 带状态分片"场景;
//   - WeightedRoundRobin:平滑加权轮询(nginx SWRR),按权重比例均匀分发,
//     避免低权重节点被连续命中,适合"按容量分配流量"场景;
//   - RoundRobin:无权重轮询,atomic 游标,无锁高吞吐,适合节点等价的场景。
//
// 节点由调用方提供,实现 Node 接口(ID 用于虚拟节点命名,Weight 用于权重计算)。
// 算法本身是纯计算,并发安全:ConsistentHash 构建后只读;WeightedRoundRobin
// 的 Next 内部加锁;RoundRobin 用 atomic 游标无锁。
//
// 零值不可用,用 NewConsistentHash / NewWeightedRoundRobin / NewRoundRobin 构造。
package loadbalance

import (
	"hash/maphash"
	"sort"
	"sync"
	"sync/atomic"
)

// Node 是负载均衡的节点。ID 在一个 Balancer 内唯一;Weight 为非负权重。
type Node[T any] interface {
	ID() string
	Weight() int
}

// nodeItem 把 Node 与其权重打包,避免反复调用接口方法。
type nodeItem[T any] struct {
	node   T
	id     string
	weight int
}

var hashSeed = maphash.MakeSeed()

// ===== ConsistentHash =====

// consistentHashOption 用函数式 Option 配置一致性哈希。
type consistentHashConfig struct {
	virtualFactor uint32 // 每个真实节点的虚拟节点倍数
	weighted      bool   // 是否按 Weight 放大虚拟节点数
	replica       uint32 // 每次查询返回的副本数(含主节点)
}

// ConsistentHashOption 是一致性哈希的配置选项。
type ConsistentHashOption[T any] func(*consistentHashConfig)

// WithVirtualFactor 设置每个真实节点的虚拟节点倍数(默认 100)。
// 值越大,负载越均匀,但内存与构建成本越高。
func WithVirtualFactor[T any](n uint32) ConsistentHashOption[T] {
	return func(c *consistentHashConfig) { c.virtualFactor = n }
}

// WithWeighted 启用按权重放大:虚拟节点数 = Weight × VirtualFactor(默认启用)。
// 关闭后各节点虚拟节点数 = VirtualFactor,忽略权重。
func WithWeighted[T any](weighted bool) ConsistentHashOption[T] {
	return func(c *consistentHashConfig) { c.weighted = weighted }
}

// WithReplica 设置查询时返回的副本数(含主节点,默认 0=只返回主节点)。
// 副本用于主节点不可达时的备选,按环上顺时针方向取不重复的后续节点。
func WithReplica[T any](n uint32) ConsistentHashOption[T] {
	return func(c *consistentHashConfig) { c.replica = n }
}

// ConsistentHash 一致性哈希负载均衡器。构建后只读,并发安全。
// 零值不可用,用 NewConsistentHash 构造。
type ConsistentHash[T any] struct {
	realNodes    []nodeItem[T]
	virtualNodes []virtualNode[T]
	replica      uint32
}

type virtualNode[T any] struct {
	hash uint64
	idx  int // 指向 realNodes 的下标
}

// NewConsistentHash 创建一致性哈希。nodes 为空时返回空 Balancer(Get 返回零值)。
func NewConsistentHash[T any](nodes []T, opts ...ConsistentHashOption[T]) *ConsistentHash[T] {
	cfg := consistentHashConfig{virtualFactor: 100, weighted: true, replica: 0}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.virtualFactor == 0 {
		cfg.virtualFactor = 100
	}
	ch := &ConsistentHash[T]{replica: cfg.replica}
	for _, n := range nodes {
		nd, ok := any(n).(Node[T])
		if !ok {
			continue
		}
		ch.realNodes = append(ch.realNodes, nodeItem[T]{node: n, id: nd.ID(), weight: nd.Weight()})
	}
	ch.virtualNodes = ch.buildVirtualNodes(cfg)
	return ch
}

func (c *ConsistentHash[T]) buildVirtualNodes(cfg consistentHashConfig) []virtualNode[T] {
	total := 0
	for _, rn := range c.realNodes {
		total += c.vNodeLen(rn, cfg)
	}
	if total == 0 {
		return nil
	}
	ret := make([]virtualNode[T], 0, total)
	// 用 "id#i" 作为虚拟节点 key,maphash 计算哈希。
	var h maphash.Hash
	h.SetSeed(hashSeed)
	for i, rn := range c.realNodes {
		vLen := c.vNodeLen(rn, cfg)
		for j := range vLen {
			h.Reset()
			h.WriteString(rn.id)
			h.WriteByte('#')
			writeInt(&h, j)
			ret = append(ret, virtualNode[T]{hash: h.Sum64(), idx: i})
		}
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].hash < ret[j].hash })
	return ret
}

func (c *ConsistentHash[T]) vNodeLen(rn nodeItem[T], cfg consistentHashConfig) int {
	if rn.weight <= 0 {
		return 0
	}
	if cfg.weighted {
		return rn.weight * int(cfg.virtualFactor)
	}
	return int(cfg.virtualFactor)
}

// Get 按 key 返回主节点。key 为空或无节点时返回零值 + false。
func (c *ConsistentHash[T]) Get(key string) (T, bool) {
	var zero T
	if len(c.virtualNodes) == 0 || key == "" {
		return zero, false
	}
	idx := c.search(maphash.String(hashSeed, key))
	return c.realNodes[c.virtualNodes[idx].idx].node, true
}

// GetReplicas 按 key 返回主节点 + 顺时针方向的不重复后续节点,共 n 个(不足则返回实际数量)。
// n <= 0 时等价于 Get。
func (c *ConsistentHash[T]) GetReplicas(key string, n int) []T {
	if n <= 0 {
		if v, ok := c.Get(key); ok {
			return []T{v}
		}
		return nil
	}
	if len(c.virtualNodes) == 0 || key == "" {
		return nil
	}
	if n > len(c.realNodes) {
		n = len(c.realNodes)
	}
	start := c.search(maphash.String(hashSeed, key))
	used := make(map[int]struct{}, n)
	out := make([]T, 0, n)
	vLen := len(c.virtualNodes)
	for i := 0; i < vLen && len(out) < n; i++ {
		idx := c.virtualNodes[(start+i)%vLen].idx
		if _, ok := used[idx]; ok {
			continue
		}
		used[idx] = struct{}{}
		out = append(out, c.realNodes[idx].node)
	}
	return out
}

// search 返回第一个 hash > key 的虚拟节点下标(环回绕)。
func (c *ConsistentHash[T]) search(key uint64) int {
	idx := sort.Search(len(c.virtualNodes), func(i int) bool {
		return c.virtualNodes[i].hash > key
	})
	if idx == len(c.virtualNodes) {
		idx = 0
	}
	return idx
}

// Nodes 返回所有真实节点(构建时的快照)。
func (c *ConsistentHash[T]) Nodes() []T {
	out := make([]T, len(c.realNodes))
	for i, rn := range c.realNodes {
		out[i] = rn.node
	}
	return out
}

func writeInt(h *maphash.Hash, n int) {
	if n == 0 {
		h.WriteByte('0')
		return
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	h.Write(buf[pos:])
}

// ===== WeightedRoundRobin =====

// WeightedRoundRobin 平滑加权轮询(nginx SWRR)。
// 算法:每轮所有节点 current += weight,选 current 最大者,选中者 current -= totalWeight。
// 结果是按权重比例均匀分布,且避免低权重节点被连续命中。
// 并发安全。零值不可用,用 NewWeightedRoundRobin 构造。
type WeightedRoundRobin[T any] struct {
	mu     sync.Mutex
	nodes  []wrrNode[T]
	weight []int // 原始权重快照,用于 Reset
}

type wrrNode[T any] struct {
	node    T
	weight  int
	current int
}

// NewWeightedRoundRobin 创建加权轮询。weight<=0 的节点被忽略。
// 节点为空时 Next 返回零值 + false。
func NewWeightedRoundRobin[T any](nodes []T) *WeightedRoundRobin[T] {
	w := &WeightedRoundRobin[T]{}
	for _, n := range nodes {
		nd, ok := any(n).(Node[T])
		if !ok {
			continue
		}
		weight := nd.Weight()
		if weight <= 0 {
			continue
		}
		w.nodes = append(w.nodes, wrrNode[T]{node: n, weight: weight})
		w.weight = append(w.weight, weight)
	}
	return w
}

// Next 返回下一个节点(SWRR)。无节点时返回零值 + false。
func (w *WeightedRoundRobin[T]) Next() (T, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var zero T
	if len(w.nodes) == 0 {
		return zero, false
	}
	totalWeight := 0
	var selected *wrrNode[T]
	maxCurrent := 0
	for i := range w.nodes {
		w.nodes[i].current += w.nodes[i].weight
		totalWeight += w.nodes[i].weight
		if selected == nil || w.nodes[i].current > maxCurrent {
			selected = &w.nodes[i]
			maxCurrent = w.nodes[i].current
		}
	}
	if selected == nil {
		return zero, false
	}
	selected.current -= totalWeight
	return selected.node, true
}

// Reset 清空内部 current 状态,回到初始轮次。
func (w *WeightedRoundRobin[T]) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.nodes {
		w.nodes[i].current = 0
	}
}

// Update 用新节点列表重建权重表。适用于服务列表变化(节点增删/权重变更)。
// 重建后 current 状态清零,从新一轮开始。weight<=0 的节点被忽略。
func (w *WeightedRoundRobin[T]) Update(nodes []T) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nodes = w.nodes[:0]
	w.weight = w.weight[:0]
	for _, n := range nodes {
		nd, ok := any(n).(Node[T])
		if !ok {
			continue
		}
		weight := nd.Weight()
		if weight <= 0 {
			continue
		}
		w.nodes = append(w.nodes, wrrNode[T]{node: n, weight: weight})
		w.weight = append(w.weight, weight)
	}
}

// Nodes 返回所有节点(构建时的快照)。
func (w *WeightedRoundRobin[T]) Nodes() []T {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]T, len(w.nodes))
	for i, n := range w.nodes {
		out[i] = n.node
	}
	return out
}

// ===== RoundRobin =====

// RoundRobin 无权重轮询。atomic 游标自增取模,读路径无锁高吞吐;
// nodes 快照用 RWMutex 保护,以支持 Update 与 Next 并发(服务列表热更新场景)。
// 适合节点等价(无权重区分)的场景。并发安全。零值不可用,用 NewRoundRobin 构造。
type RoundRobin[T any] struct {
	mu    sync.RWMutex
	nodes []T
	index atomic.Int64
}

// NewRoundRobin 创建轮询。nodes 为空时 Next 返回零值 + false。
func NewRoundRobin[T any](nodes []T) *RoundRobin[T] {
	r := &RoundRobin[T]{}
	r.Update(nodes)
	return r
}

// Update 用新节点列表替换。适用于服务列表变化(节点增删)。
func (r *RoundRobin[T]) Update(nodes []T) {
	next := make([]T, len(nodes))
	copy(next, nodes)
	r.mu.Lock()
	r.nodes = next
	r.mu.Unlock()
}

// Next 返回下一个节点(轮询)。无节点时返回零值 + false。
func (r *RoundRobin[T]) Next() (T, bool) {
	var zero T
	r.mu.RLock()
	nodes := r.nodes
	r.mu.RUnlock()
	if len(nodes) == 0 {
		return zero, false
	}
	idx := r.index.Add(1)
	return nodes[int(idx)%len(nodes)], true
}

// Nodes 返回所有节点(当前快照)。
func (r *RoundRobin[T]) Nodes() []T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]T, len(r.nodes))
	copy(out, r.nodes)
	return out
}
