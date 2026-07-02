// Package counter 提供按 key 的滑动窗口计数与时间窗配额,纯内存实现。
//
// 与 pkg/ratelimit 的区别(互补):
//   - ratelimit 控制"速率"(令牌桶:每秒 N 个、匀速放行),关心节奏;
//   - counter 控制"窗口内累计次数"(滑动窗口:某时间段内总共 N 次,不管节奏),
//     关心总量。典型:每日抽卡 100 次、1 分钟弹幕 ≤ 60 条、活动限领 3 次、
//     点赞/关注防刷。这类"配额"用令牌桶表达并不自然,用时间窗计数才贴切。
//
// 实现:每个 key 一个环形桶(把 window 切成 buckets 份),Incr 落到当前时间对应
// 的桶;Count 求和所有未过期桶。滑动而非固定窗口——避免固定窗口临界点的双倍突发。
// bucket 数越多,窗口滑动越平滑,内存与求和成本越高(默认 10)。
//
// 并发安全(分片锁减少争用)。零值不可用,用 New 构造;Stop 后 gc goroutine 退出。
package counter

import (
	"hash/maphash"
	"sync"
	"time"
)

// shardCount 分片数,降低高并发下的锁争用。必须是 2 的幂(用位与取模)。
const shardCount = 16

// config 配置。
type config struct {
	window     time.Duration
	buckets    int
	gcInterval time.Duration
}

// Option 配置 Counter。
type Option func(*config)

// WithBuckets 设置窗口切分的桶数(默认 10)。越大滑动越平滑,成本越高。
func WithBuckets(n int) Option { return func(c *config) { c.buckets = n } }

// WithGCInterval 设置空闲 key 清扫间隔(默认取 window,至少 1s)。
func WithGCInterval(d time.Duration) Option { return func(c *config) { c.gcInterval = d } }

// keyState 单个 key 的环形桶计数。
type keyState struct {
	counts   []int64 // 环形桶,每桶一段时间的累计
	bucketAt []int64 // 每桶对应的"桶起始时刻"(unix nano),用于判断桶是否过期
	lastSeen int64   // 最后访问时刻(unix nano),供 gc 清理空闲 key
}

type shard struct {
	mu   sync.Mutex
	keys map[string]*keyState
}

// Counter 滑动窗口计数器。按 key 维护独立的时间窗累计。
// 零值不可用,用 New 构造。并发安全。
type Counter struct {
	window     time.Duration
	buckets    int
	bucketDur  time.Duration
	gcInterval time.Duration

	seed   maphash.Seed
	shards [shardCount]*shard

	stopCh chan struct{}
	stop   sync.Once
}

// New 创建滑动窗口计数器。window 为统计窗口长度(如 time.Minute、24*time.Hour)。
func New(window time.Duration, opts ...Option) *Counter {
	cfg := config{window: window, buckets: 10}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.window <= 0 {
		cfg.window = time.Minute
	}
	if cfg.buckets <= 0 {
		cfg.buckets = 10
	}
	if cfg.gcInterval <= 0 {
		cfg.gcInterval = cfg.window
	}
	if cfg.gcInterval < time.Second {
		cfg.gcInterval = time.Second
	}
	c := &Counter{
		window:     cfg.window,
		buckets:    cfg.buckets,
		bucketDur:  cfg.window / time.Duration(cfg.buckets),
		gcInterval: cfg.gcInterval,
		seed:       maphash.MakeSeed(),
		stopCh:     make(chan struct{}),
	}
	for i := range c.shards {
		c.shards[i] = &shard{keys: make(map[string]*keyState)}
	}
	go c.gc()
	return c
}

func (c *Counter) shardFor(key string) *shard {
	h := maphash.String(c.seed, key)
	return c.shards[h&(shardCount-1)]
}

// Incr 给 key 增加 n(n 可为任意正整数,如礼物数量)。返回增加后的当前窗口累计值。
func (c *Counter) Incr(key string, n int64) int64 {
	return c.add(key, n)
}

// Add 是 Incr 的别名,语义更通用。
func (c *Counter) Add(key string, n int64) int64 { return c.add(key, n) }

func (c *Counter) add(key string, n int64) int64 {
	now := time.Now().UnixNano()
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	ks := s.keys[key]
	if ks == nil {
		ks = &keyState{
			counts:   make([]int64, c.buckets),
			bucketAt: make([]int64, c.buckets),
		}
		s.keys[key] = ks
	}
	ks.lastSeen = now

	idx, start := c.bucketIndex(now)
	// 若该桶槽位对应的是旧时间段,先清零(环形复用)。
	if ks.bucketAt[idx] != start {
		ks.bucketAt[idx] = start
		ks.counts[idx] = 0
	}
	ks.counts[idx] += n

	return c.sumLocked(ks, now)
}

// Count 返回 key 在当前滑动窗口内的累计值(不修改)。
func (c *Counter) Count(key string) int64 {
	now := time.Now().UnixNano()
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.keys[key]
	if ks == nil {
		return 0
	}
	return c.sumLocked(ks, now)
}

// Allow 判断 key 再增加 n 后是否仍不超过 limit:不超则增加并返回 true,
// 超过则不增加并返回 false。用于"窗口内配额"控制(如每日抽卡上限)。
func (c *Counter) Allow(key string, n, limit int64) bool {
	now := time.Now().UnixNano()
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	ks := s.keys[key]
	if ks == nil {
		ks = &keyState{
			counts:   make([]int64, c.buckets),
			bucketAt: make([]int64, c.buckets),
		}
		s.keys[key] = ks
	}
	ks.lastSeen = now

	if c.sumLocked(ks, now)+n > limit {
		return false
	}
	idx, start := c.bucketIndex(now)
	if ks.bucketAt[idx] != start {
		ks.bucketAt[idx] = start
		ks.counts[idx] = 0
	}
	ks.counts[idx] += n
	return true
}

// Reset 清零 key 的计数(删除其状态)。
func (c *Counter) Reset(key string) {
	s := c.shardFor(key)
	s.mu.Lock()
	delete(s.keys, key)
	s.mu.Unlock()
}

// bucketIndex 返回 now 对应的环形桶下标与该桶的起始时刻(按 bucketDur 对齐)。
func (c *Counter) bucketIndex(now int64) (idx int, start int64) {
	slot := now / int64(c.bucketDur)
	idx = int(slot % int64(c.buckets))
	start = slot * int64(c.bucketDur)
	return idx, start
}

// sumLocked 求和所有仍在窗口内的桶。调用方持 shard 锁。
func (c *Counter) sumLocked(ks *keyState, now int64) int64 {
	cutoff := now - int64(c.window)
	var sum int64
	for i := range ks.counts {
		if ks.bucketAt[i] > cutoff {
			sum += ks.counts[i]
		}
	}
	return sum
}

// Stop 停止 gc goroutine。幂等。
func (c *Counter) Stop() {
	c.stop.Do(func() { close(c.stopCh) })
}

// gc 周期清理"最后访问已超过一个窗口"的空闲 key,回收内存。
func (c *Counter) gc() {
	ticker := time.NewTicker(c.gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().UnixNano() - int64(c.window)
			for _, s := range c.shards {
				s.mu.Lock()
				for k, ks := range s.keys {
					if ks.lastSeen < cutoff {
						delete(s.keys, k)
					}
				}
				s.mu.Unlock()
			}
		case <-c.stopCh:
			return
		}
	}
}
