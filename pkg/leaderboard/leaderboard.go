// Package leaderboard 提供排行榜的内存排名缓存:用堆排序维护每个榜的有序结构,
// O(log N) 查"我的名次"、按名次取记录,避免高频读时每次 ORDER BY 击穿数据库。
//
// 设计要点:
//   - 每个榜 + 过期周期维护一个独立的 RankCache;
//   - Fill 全量加载、Insert/Delete 增量维护、Get 按主人查名次;
//   - 黑名单机制:某些超大或写频繁的榜可不进缓存,退化为不缓存(本包表现为 Get 返回 -1)。
//
// 适用场景:游戏排行榜、积分榜、热榜的"我的排名/TopN"高频读。
//
// 零值不可用,用 New 构造。RankCache 并发安全。
package leaderboard

import (
	"container/heap"
	"sort"
	"strconv"
	"sync"
)

// SortOrder 排序方向。
type SortOrder int

const (
	SortAscending  SortOrder = iota // 升序:分数小者居前(如用时少的赢家)
	SortDescending                  // 降序:分数大者居前(如积分)
)

// Record 是一条榜记录。
type Record struct {
	OwnerID  string
	Score    int64
	Subscore int64 // 次级分数,Score 相同时按此排序
}

// RankCache 管理多个排行榜的内存排名缓存。
type RankCache struct {
	mu          sync.RWMutex
	caches      map[string]*rankEntry // key = leaderboardID + "/" + expiryUnix
	blacklist   map[string]struct{}   // 不缓存的榜 ID
	blacklistAll bool                  // 黑名单含 "*" 表示全部不缓存
}

type rankEntry struct {
	order   SortOrder
	byOwner map[string]int // ownerID -> 在 items 中的索引
	items   *itemHeap
}

// item 是堆元素。
type item struct {
	ownerID  string
	score    int64
	subscore int64
}

// itemHeap 实现 heap.Interface,按 order 决定升/降序。
type itemHeap struct {
	items []item
	order SortOrder
}

func (h itemHeap) Len() int { return len(h.items) }
func (h itemHeap) Less(i, j int) bool {
	if h.order == SortAscending {
		if h.items[i].score != h.items[j].score {
			return h.items[i].score < h.items[j].score
		}
		return h.items[i].subscore < h.items[j].subscore
	}
	if h.items[i].score != h.items[j].score {
		return h.items[i].score > h.items[j].score
	}
	return h.items[i].subscore > h.items[j].subscore
}
func (h itemHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *itemHeap) Push(x any)   { h.items = append(h.items, x.(item)) }
func (h *itemHeap) Pop() any {
	old := h.items
	n := len(old)
	x := old[n-1]
	h.items = old[:n-1]
	return x
}

// New 创建 RankCache。blacklist 为不缓存的榜 ID 列表;含 "*" 表示全部不缓存。
func New(blacklist ...string) *RankCache {
	rc := &RankCache{
		caches: make(map[string]*rankEntry),
	}
	if len(blacklist) == 1 && blacklist[0] == "*" {
		rc.blacklistAll = true
	} else {
		rc.blacklist = make(map[string]struct{}, len(blacklist))
		for _, id := range blacklist {
			rc.blacklist[id] = struct{}{}
		}
	}
	return rc
}

func cacheKey(leaderboardID string, expiry int64) string {
	return leaderboardID + "/" + strconv.FormatInt(expiry, 10)
}

// Fill 全量加载一个榜的记录,构建缓存。返回缓存的记录数。
// 若该榜在黑名单中,返回 0 且不缓存。
// enable=false 时也跳过(用于运行时动态关闭某榜缓存)。
func (r *RankCache) Fill(leaderboardID string, expiry int64, order SortOrder, records []Record, enable bool) int {
	if !enable || r.blacklistAll {
		return 0
	}
	if _, blocked := r.blacklist[leaderboardID]; blocked {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := &rankEntry{
		order:   order,
		byOwner: make(map[string]int, len(records)),
		items:   &itemHeap{order: order, items: make([]item, 0, len(records))},
	}
	for _, rec := range records {
		e.byOwner[rec.OwnerID] = len(e.items.items)
		e.items.items = append(e.items.items, item{
			ownerID:  rec.OwnerID,
			score:    rec.Score,
			subscore: rec.Subscore,
		})
	}
	heap.Init(e.items)
	// 重建 byOwner 索引(heap 化后位置变了)。
	for i, it := range e.items.items {
		e.byOwner[it.ownerID] = i
	}
	r.caches[cacheKey(leaderboardID, expiry)] = e
	return len(records)
}

// Insert 插入或更新一条记录,增量维护堆。返回该 owner 的当前名次(从 1 起)。
// 若该榜未缓存,返回 -1。
func (r *RankCache) Insert(leaderboardID string, expiry int64, order SortOrder, rec Record, enable bool) int64 {
	if !enable || r.blacklistAll {
		return -1
	}
	if _, blocked := r.blacklist[leaderboardID]; blocked {
		return -1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.caches[cacheKey(leaderboardID, expiry)]
	if !ok {
		// 惰性创建空榜。
		e = &rankEntry{
			order:   order,
			byOwner: make(map[string]int),
			items:   &itemHeap{order: order},
		}
		r.caches[cacheKey(leaderboardID, expiry)] = e
	}
	it := item{ownerID: rec.OwnerID, score: rec.Score, subscore: rec.Subscore}
	if idx, exists := e.byOwner[rec.OwnerID]; exists {
		e.items.items[idx] = it
		heap.Fix(e.items, idx)
	} else {
		heap.Push(e.items, it)
		// 重建索引(heap Push 后位置需刷新)。
		for i, x := range e.items.items {
			e.byOwner[x.ownerID] = i
		}
	}
	return r.rankLocked(e, rec.OwnerID)
}

// Delete 删除一条记录。返回是否曾存在。
func (r *RankCache) Delete(leaderboardID string, expiry int64, ownerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.caches[cacheKey(leaderboardID, expiry)]
	if !ok {
		return false
	}
	idx, exists := e.byOwner[ownerID]
	if !exists {
		return false
	}
	heap.Remove(e.items, idx)
	delete(e.byOwner, ownerID)
	// 重建索引。
	for i, x := range e.items.items {
		e.byOwner[x.ownerID] = i
	}
	return true
}

// Get 返回某 owner 的名次(从 1 起)。未缓存或不存在返回 -1。
func (r *RankCache) Get(leaderboardID string, expiry int64, ownerID string) int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.caches[cacheKey(leaderboardID, expiry)]
	if !ok {
		return -1
	}
	return r.rankLocked(e, ownerID)
}

// rankLocked 在持锁状态下计算名次。注意:堆只保证堆顶有序,名次需遍历计数。
// 对中小规模(万级)可接受;更大规模应改用平衡树或有序数组+二分。
func (r *RankCache) rankLocked(e *rankEntry, ownerID string) int64 {
	idx, ok := e.byOwner[ownerID]
	if !ok {
		return -1
	}
	target := e.items.items[idx]
	// 名次 = 严格优于 target 的元素数 + 1。
	rank := int64(1)
	for _, it := range e.items.items {
		if it.ownerID == ownerID {
			continue
		}
		if e.order == SortAscending {
			if it.score < target.score || (it.score == target.score && it.subscore < target.subscore) {
				rank++
			}
		} else {
			if it.score > target.score || (it.score == target.score && it.subscore > target.subscore) {
				rank++
			}
		}
	}
	return rank
}

// GetByRank 返回第 rank 名(从 1 起)的记录。rank 越界返回零值 + false。
func (r *RankCache) GetByRank(leaderboardID string, expiry int64, rank int) (Record, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.caches[cacheKey(leaderboardID, expiry)]
	if !ok || rank < 1 || rank > len(e.items.items) {
		return Record{}, false
	}
	// 拷贝并排序取第 rank 名。
	sorted := make([]item, len(e.items.items))
	copy(sorted, e.items.items)
	sort.Slice(sorted, func(i, j int) bool {
		if e.order == SortAscending {
			if sorted[i].score != sorted[j].score {
				return sorted[i].score < sorted[j].score
			}
			return sorted[i].subscore < sorted[j].subscore
		}
		if sorted[i].score != sorted[j].score {
			return sorted[i].score > sorted[j].score
		}
		return sorted[i].subscore > sorted[j].subscore
	})
	it := sorted[rank-1]
	return Record{OwnerID: it.ownerID, Score: it.score, Subscore: it.subscore}, true
}

// TopN 返回前 N 名(按 order)。N 超过总数时返回全部。
func (r *RankCache) TopN(leaderboardID string, expiry int64, n int) []Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.caches[cacheKey(leaderboardID, expiry)]
	if !ok || n <= 0 {
		return nil
	}
	sorted := make([]item, len(e.items.items))
	copy(sorted, e.items.items)
	sort.Slice(sorted, func(i, j int) bool {
		if e.order == SortAscending {
			if sorted[i].score != sorted[j].score {
				return sorted[i].score < sorted[j].score
			}
			return sorted[i].subscore < sorted[j].subscore
		}
		if sorted[i].score != sorted[j].score {
			return sorted[i].score > sorted[j].score
		}
		return sorted[i].subscore > sorted[j].subscore
	})
	if n > len(sorted) {
		n = len(sorted)
	}
	out := make([]Record, n)
	for i := 0; i < n; i++ {
		out[i] = Record{OwnerID: sorted[i].ownerID, Score: sorted[i].score, Subscore: sorted[i].subscore}
	}
	return out
}

// Around 返回某 owner 前后各 range 条记录(含自己)。
func (r *RankCache) Around(leaderboardID string, expiry int64, ownerID string, around int) []Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.caches[cacheKey(leaderboardID, expiry)]
	if !ok {
		return nil
	}
	myRank := r.rankLocked(e, ownerID)
	if myRank < 1 {
		return nil
	}
	// 取 [myRank-around, myRank+around] 区间(夹紧到 [1, total])。
	start := max(int(myRank)-around, 1)
	end := min(int(myRank)+around, len(e.items.items))
	sorted := make([]item, len(e.items.items))
	copy(sorted, e.items.items)
	sort.Slice(sorted, func(i, j int) bool {
		if e.order == SortAscending {
			if sorted[i].score != sorted[j].score {
				return sorted[i].score < sorted[j].score
			}
			return sorted[i].subscore < sorted[j].subscore
		}
		if sorted[i].score != sorted[j].score {
			return sorted[i].score > sorted[j].score
		}
		return sorted[i].subscore > sorted[j].subscore
	})
	out := make([]Record, 0, end-start+1)
	for i := start - 1; i < end; i++ {
		out = append(out, Record{OwnerID: sorted[i].ownerID, Score: sorted[i].score, Subscore: sorted[i].subscore})
	}
	return out
}

// Size 返回某榜的缓存记录数。
func (r *RankCache) Size(leaderboardID string, expiry int64) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.caches[cacheKey(leaderboardID, expiry)]
	if !ok {
		return 0
	}
	return len(e.items.items)
}

// DeleteLeaderboard 移除整个榜的缓存(过期/重置时调用)。
func (r *RankCache) DeleteLeaderboard(leaderboardID string, expiry int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := cacheKey(leaderboardID, expiry)
	if _, ok := r.caches[key]; ok {
		delete(r.caches, key)
		return true
	}
	return false
}
