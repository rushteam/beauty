package shard

import (
	"math"
	"sort"
	"sync"
)

// Jump 是 Google 的 Jump Consistent Hash:把 key 映射到 [0, buckets) 的一个桶,几乎无内存、
// 无需哈希环,且桶数从 n 变到 n+1 时,只有约 1/(n+1) 的 key 会迁移(最小化再分布)。
// 适合桶(分片/副本)按 0..n-1 连续编号、且只在尾部增减的场景。buckets<=0 返回 0。
//
// 局限:桶必须是连续整数编号,不能任意增删中间桶(那种场景用一致性哈希环或 Rendezvous)。
func Jump(key uint64, buckets int) int {
	if buckets <= 0 {
		return 0
	}
	var b, j int64 = -1, 0
	for j < int64(buckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * (float64(int64(1)<<31) / float64((key>>33)+1)))
	}
	return int(b)
}

// Rendezvous 是 Rendezvous(HRW,Highest Random Weight)哈希:对每个 key,选出使
// hash(member, key) 加权得分最高的成员。相比一致性哈希环,它无需虚拟节点、实现更小,
// 天然支持权重,且增删任一成员只影响原属于/将属于该成员的 key(其余不动)。
// 适合成员可任意增删(非连续编号)、需加权、且成员数不特别大的场景(每次 Pick 是 O(成员数))。
// 并发安全。零值不可用,用 NewRendezvous 构造。
type Rendezvous struct {
	mu      sync.RWMutex
	members []Member
}

// NewRendezvous 创建 HRW 选择器。
func NewRendezvous(members ...Member) *Rendezvous {
	r := &Rendezvous{}
	r.Update(members)
	return r
}

// Update 替换成员列表。
func (r *Rendezvous) Update(members []Member) {
	next := make([]Member, len(members))
	copy(next, members)
	r.mu.Lock()
	r.members = next
	r.mu.Unlock()
}

// Pick 返回 key 对应得分最高的成员。无成员时返回 nil + false。
func (r *Rendezvous) Pick(key string) (Member, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var best Member
	var bestScore float64
	for _, m := range r.members {
		s := score(m, key)
		if best == nil || s > bestScore {
			best, bestScore = m, s
		}
	}
	return best, best != nil
}

// PickN 返回 key 得分最高的前 n 个成员(按得分降序;不足则返回全部)。
// 用于副本放置:主 + 若干备。
func (r *Rendezvous) PickN(key string, n int) []Member {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n <= 0 || len(r.members) == 0 {
		return nil
	}
	type scored struct {
		m Member
		s float64
	}
	all := make([]scored, len(r.members))
	for i, m := range r.members {
		all[i] = scored{m, score(m, key)}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].s > all[j].s })
	if n > len(all) {
		n = len(all)
	}
	out := make([]Member, n)
	for i := 0; i < n; i++ {
		out[i] = all[i].m
	}
	return out
}

// Members 返回当前成员快照。
func (r *Rendezvous) Members() []Member {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Member, len(r.members))
	copy(out, r.members)
	return out
}

// score 计算成员对某 key 的加权 HRW 得分:把 hash 归一到 (0,1),用 -weight/ln(h) 加权
// (weight 越大得分期望越高;h 越接近 1 得分越大)。
func score(m Member, key string) float64 {
	w := m.Weight()
	if w < 1 {
		w = 1
	}
	h := hash64(m.ID(), key)
	// 归一到 (0,1):取高 53 位落到 [0,1),再抬升避免取到 0。
	hf := (float64(h>>11) + 1) / (float64(uint64(1)<<53) + 1)
	return float64(w) / -math.Log(hf)
}

// hash64 对 (id, key) 做确定性哈希(FNV-1a + splitmix64 收敛)。
func hash64(id, key string) uint64 {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(id); i++ {
		h = (h ^ uint64(id[i])) * prime
	}
	h = (h ^ 0) * prime // 分隔符,降低 id|key 拼接歧义
	for i := 0; i < len(key); i++ {
		h = (h ^ uint64(key[i])) * prime
	}
	// splitmix64 finalizer
	h += 0x9e3779b97f4a7c15
	h = (h ^ (h >> 30)) * 0xbf58476d1ce4e5b9
	h = (h ^ (h >> 27)) * 0x94d049bb133111eb
	return h ^ (h >> 31)
}
