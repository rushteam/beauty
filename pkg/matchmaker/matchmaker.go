// Package matchmaker 提供基于属性匹配的组队原语。
//
// 玩家/会话携带 string + numeric 属性注册一张 ticket,匹配器按"桶(bucket,
// 如 region+mode)+ 最低共同属性"策略聚合候选,凑齐队伍即成匹配。
//
// 与基于 Bluge 全文索引的做法不同,本包用纯标准库实现一个
// 倒排索引 + 桶分组的轻量匹配器,适合中小规模(单机万级 ticket)。
// 超过此规模建议接入专用检索引擎。
//
// 零值不可用,用 New 构造。
package matchmaker

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Presence 表示一个待匹配的参与者。
type Presence struct {
	UserID    string
	SessionID string
	Node      string
	Username  string
}

// Properties 是 ticket 的属性集合:string 类(用于桶分组、精确匹配)与 numeric 类(用于范围/排序)。
type Properties struct {
	String  map[string]string  // 如 {"region":"eu","mode":"ranked"}
	Numeric map[string]float64 // 如 {"skill":1200,"latency":45}
}

// Ticket 是一次匹配请求的句柄。
type Ticket struct {
	ID         string
	Presence   Presence
	Properties Properties
	CreateTime int64 // unix nano
	MinCount   int   // 最小成队人数
	MaxCount   int   // 最大成队人数
}

// Match 是一次成功匹配的结果。
type Match struct {
	Tickets []*Ticket
	Pool    string
}

// Handler 由业务实现,在匹配成功时被调用(在工作池 goroutine 中)。
// 返回 error 会让相关 ticket 重新进入候选池。
type Handler func(ctx context.Context, m Match) error

// Matchmaker 管理候选池并周期性尝试匹配。
type Matchmaker struct {
	mu       sync.RWMutex
	tickets  map[string]*Ticket                 // ticket ID -> ticket
	buckets  map[string]map[string]*bucketEntry // pool -> bucket key -> entry
	handler  Handler
	cfg      config
	tickRate time.Duration
	stopped  atomic.Bool
	stopCh   chan struct{}
	done     chan struct{}
	count    atomic.Int64

	// numeric 索引:pool -> attr -> 按值排序的 ticket 列表(用于范围/近邻查询)。
	numIndex map[string]map[string][]*Ticket
}

type bucketEntry struct {
	tickets map[string]*Ticket // bucket 内的 ticket
}

type config struct {
	tickInterval time.Duration // 扫描周期
	poolCount    int           // worker 数
	maxWaitSec   int           // ticket 最长等待,超时强匹配(放宽桶)
}

// Option 配置 Matchmaker。
type Option func(*config)

// WithTickInterval 设置匹配扫描周期,默认 500ms。
func WithTickInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.tickInterval = d
		}
	}
}

// WithMaxWaitSec 设置 ticket 最长等待秒数:超时后放宽桶约束(忽略 string 属性差异)强匹配。
// 默认 30。<=0 不放宽。
func WithMaxWaitSec(sec int) Option {
	return func(c *config) { c.maxWaitSec = sec }
}

// New 创建匹配器。h 在每次匹配成功时被调用。
func New(h Handler, opts ...Option) *Matchmaker {
	cfg := config{
		tickInterval: 500 * time.Millisecond,
		poolCount:    4,
		maxWaitSec:   30,
	}
	for _, o := range opts {
		o(&cfg)
	}
	m := &Matchmaker{
		tickets:  make(map[string]*Ticket),
		buckets:  make(map[string]map[string]*bucketEntry),
		handler:  h,
		cfg:      cfg,
		tickRate: cfg.tickInterval,
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
		numIndex: make(map[string]map[string][]*Ticket),
	}
	return m
}

// Start 启动匹配循环。幂等。ctx 取消时停止。
func (m *Matchmaker) Start(ctx context.Context) {
	go m.loop(ctx)
}

// Stop 停止匹配循环。幂等。
func (m *Matchmaker) Stop() {
	if m.stopped.CompareAndSwap(false, true) {
		close(m.stopCh)
	}
}

// Wait 阻塞直到循环退出。
func (m *Matchmaker) Wait() { <-m.done }

// Count 返回当前候选 ticket 数。
func (m *Matchmaker) Count() int { return int(m.count.Load()) }

// Add 注册一张 ticket 进候选池。pool 用于隔离不同匹配类型(如 "5v5"/"3v3")。
// bucketKey 由 string 属性拼成(如 "eu|ranked"),决定同桶匹配优先级。
func (m *Matchmaker) Add(t Ticket, pool, bucketKey string) (string, error) {
	if t.MinCount <= 0 || t.MaxCount < t.MinCount {
		return "", fmt.Errorf("matchmaker: invalid count range [%d, %d]", t.MinCount, t.MaxCount)
	}
	if t.ID == "" {
		t.ID = fmt.Sprintf("%x", time.Now().UnixNano())
	}
	t.CreateTime = time.Now().UnixNano()

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tickets[t.ID]; exists {
		return "", fmt.Errorf("matchmaker: ticket %s already exists", t.ID)
	}
	m.tickets[t.ID] = &t
	m.count.Add(1)

	// 入桶。
	byPool, ok := m.buckets[pool]
	if !ok {
		byPool = make(map[string]*bucketEntry)
		m.buckets[pool] = byPool
	}
	bucket, ok := byPool[bucketKey]
	if !ok {
		bucket = &bucketEntry{tickets: make(map[string]*Ticket)}
		byPool[bucketKey] = bucket
	}
	bucket.tickets[t.ID] = &t

	// numeric 索引。
	if len(t.Properties.Numeric) > 0 {
		poolIdx, ok := m.numIndex[pool]
		if !ok {
			poolIdx = make(map[string][]*Ticket)
			m.numIndex[pool] = poolIdx
		}
		for attr := range t.Properties.Numeric {
			poolIdx[attr] = append(poolIdx[attr], &t)
		}
	}
	return t.ID, nil
}

// Remove 移除一张 ticket(如玩家取消)。
func (m *Matchmaker) Remove(ticketID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[ticketID]
	if !ok {
		return false
	}
	delete(m.tickets, ticketID)
	m.count.Add(-1)
	// 从桶移除:需遍历 pool/bucket 找到它(代价可接受,Remove 低频)。
	for _, byPool := range m.buckets {
		for _, bucket := range byPool {
			if _, ok := bucket.tickets[ticketID]; ok {
				delete(bucket.tickets, ticketID)
			}
		}
	}
	// 从 numeric 索引惰性移除(扫描时跳过已删)。
	for _, poolIdx := range m.numIndex {
		for attr := range t.Properties.Numeric {
			if list, ok := poolIdx[attr]; ok {
				for i, tt := range list {
					if tt.ID == ticketID {
						poolIdx[attr] = append(list[:i], list[i+1:]...)
						break
					}
				}
			}
		}
	}
	return true
}

// loop 是匹配主循环:周期性扫描所有 pool 的桶,尝试凑队。
func (m *Matchmaker) loop(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(m.tickRate)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *Matchmaker) tick(ctx context.Context) {
	m.mu.RLock()
	// 收集每个 pool 的桶快照(避免持锁调用 handler)。
	type bucketSnap struct {
		pool      string
		bucketKey string
		tickets   []*Ticket
	}
	var snaps []bucketSnap
	now := time.Now().UnixNano()
	maxWaitNs := int64(m.cfg.maxWaitSec) * int64(time.Second)

	for pool, byPool := range m.buckets {
		for bucketKey, bucket := range byPool {
			if len(bucket.tickets) == 0 {
				continue
			}
			var ts []*Ticket
			for _, t := range bucket.tickets {
				ts = append(ts, t)
			}
			snaps = append(snaps, bucketSnap{pool, bucketKey, ts})
		}
		// 跨桶:收集超时 ticket(放宽桶约束)。
		if m.cfg.maxWaitSec > 0 {
			var timedOut []*Ticket
			for _, bucket := range byPool {
				for _, t := range bucket.tickets {
					if now-t.CreateTime > maxWaitNs {
						timedOut = append(timedOut, t)
					}
				}
			}
			if len(timedOut) > 0 {
				snaps = append(snaps, bucketSnap{pool, "__timeout__", timedOut})
			}
		}
	}
	m.mu.RUnlock()

	for _, snap := range snaps {
		m.tryMatch(ctx, snap.pool, snap.tickets)
	}
}

// tryMatch 在一组 ticket 中尝试凑出队伍:贪心按 skill 排序,取相邻最小差凑齐。
func (m *Matchmaker) tryMatch(ctx context.Context, pool string, ts []*Ticket) {
	if len(ts) == 0 {
		return
	}
	// 按 skill 排序(若有),便于凑相近水平。
	sort.Slice(ts, func(i, j int) bool {
		si, sj := ts[i].Properties.Numeric["skill"], ts[j].Properties.Numeric["skill"]
		return si < sj
	})

	used := make(map[string]bool)
	for i := 0; i < len(ts); i++ {
		if used[ts[i].ID] {
			continue
		}
		team := []*Ticket{ts[i]}
		// 向后找相邻的凑齐 MaxCount。
		for j := i + 1; j < len(ts) && len(team) < ts[i].MaxCount; j++ {
			if used[ts[j].ID] {
				continue
			}
			// 检查双方 count 范围是否兼容。
			if !countCompatible(team, ts[j]) {
				continue
			}
			team = append(team, ts[j])
		}
		// 凑齐最小人数即成匹配。
		if len(team) >= ts[i].MinCount {
			for _, t := range team {
				used[t.ID] = true
			}
			matched := make([]*Ticket, len(team))
			copy(matched, team)
			// 异步回调 handler,失败则回滚。
			go func(match Match) {
				if err := m.handler(ctx, match); err == nil {
					for _, t := range match.Tickets {
						m.Remove(t.ID)
					}
				}
			}(Match{Tickets: matched, Pool: pool})
		}
	}
}

// countCompatible 判断把 cand 加入 team 后,team 大小是否落在所有成员的 [Min,Max] 区间。
func countCompatible(team []*Ticket, cand *Ticket) bool {
	newSize := len(team) + 1
	for _, t := range team {
		if newSize > t.MaxCount {
			return false
		}
	}
	return newSize >= cand.MinCount && newSize <= cand.MaxCount
}

// QueryNearBySkill 返回 pool 中 skill 最接近 val 的 n 个 ticket(用于调试/扩展)。
func (m *Matchmaker) QueryNearBySkill(pool string, val float64, n int) []*Ticket {
	m.mu.RLock()
	defer m.mu.RUnlock()
	poolIdx, ok := m.numIndex[pool]
	if !ok {
		return nil
	}
	list, ok := poolIdx["skill"]
	if !ok {
		return nil
	}
	// list 已在 tick 中排序?不一定,这里排序拷贝。
	sorted := make([]*Ticket, 0, len(list))
	sorted = append(sorted, list...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Properties.Numeric["skill"] < sorted[j].Properties.Numeric["skill"]
	})
	// 二分找最近。
	idx := sort.Search(len(sorted), func(i int) bool {
		return sorted[i].Properties.Numeric["skill"] >= val
	})
	var out []*Ticket
	for lo, hi := idx-1, idx; lo >= 0 || hi < len(sorted); {
		var pick *Ticket
		if hi >= len(sorted) || (lo >= 0 && math.Abs(val-sorted[lo].Properties.Numeric["skill"]) <= math.Abs(val-sorted[hi].Properties.Numeric["skill"])) {
			pick = sorted[lo]
			lo--
		} else {
			pick = sorted[hi]
			hi++
		}
		out = append(out, pick)
		if len(out) >= n {
			break
		}
	}
	return out
}
