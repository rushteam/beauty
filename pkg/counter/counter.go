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
//
// 生产多实例:默认内存实现的计数不跨实例(每台各算各的,配额可被绕过)。用
// WithStore 接入 kvstore.Store(如 Redis)后,计数跨实例一致。注意:store 模式用
// 固定窗口(每个 window 一个带 TTL 的计数 key),而非内存模式的滑动窗口——固定窗口
// 在窗口边界可能出现最多 2 倍瞬时突发,换取"用一次原子 INCR 就能跨实例计数"的简单可靠。
package counter

import (
	"context"
	"hash/maphash"
	"strconv"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/kvstore"
)

// shardCount 分片数,降低高并发下的锁争用。必须是 2 的幂(用位与取模)。
const shardCount = 16

// config 配置。
type config struct {
	window     time.Duration
	buckets    int
	gcInterval time.Duration
	store      kvstore.Store
	onStoreErr func(op, key string, err error)
}

// WithStore 让计数走外部共享存储(如 Redis 实现的 kvstore.Store),使计数跨实例一致。
// 配置后 Incr/Count/Allow 路由到 store(固定窗口),不再使用内存分片与 gc。
func WithStore(s kvstore.Store) Option { return func(c *config) { c.store = s } }

// WithOnStoreError 设置 store 操作出错时的回调(网络故障等)。默认静默;
// 出错时读操作返回 0、Allow 放行(fail-open),由此回调上报供监控。
func WithOnStoreError(fn func(op, key string, err error)) Option {
	return func(c *config) { c.onStoreErr = fn }
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

	store      kvstore.Store // 非 nil 时走共享存储(固定窗口),否则走内存分片
	onStoreErr func(op, key string, err error)

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
		store:      cfg.store,
		onStoreErr: cfg.onStoreErr,
		seed:       maphash.MakeSeed(),
		stopCh:     make(chan struct{}),
	}
	// store 模式无需内存分片与 gc goroutine。
	if c.store == nil {
		for i := range c.shards {
			c.shards[i] = &shard{keys: make(map[string]*keyState)}
		}
		go c.gc()
	}
	return c
}

// storeKey 生成 store 模式的固定窗口 key:key@window 序号。同一 window 内所有
// 请求落到同一个带 TTL 的计数键,窗口滚动时自然切到新键(旧键 TTL 到期自动清理)。
func (c *Counter) storeKey(key string, now int64) string {
	win := now / int64(c.window)
	return "cnt:" + key + ":" + strconv.FormatInt(win, 10)
}

func (c *Counter) reportErr(op, key string, err error) {
	if err != nil && c.onStoreErr != nil {
		c.onStoreErr(op, key, err)
	}
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
	if c.store != nil {
		// 固定窗口:对当前窗口的计数键原子自增,首次设置窗口长度为 TTL。
		v, err := c.store.Incr(context.Background(), c.storeKey(key, now), n, c.window)
		if err != nil {
			c.reportErr("incr", key, err)
			return 0
		}
		return v
	}
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

// Count 返回 key 在当前窗口内的累计值(不修改)。
func (c *Counter) Count(key string) int64 {
	now := time.Now().UnixNano()
	if c.store != nil {
		v, _, err := c.store.GetInt(context.Background(), c.storeKey(key, now))
		if err != nil {
			c.reportErr("get", key, err)
			return 0
		}
		return v
	}
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
//
// store 模式下用"先增后判、超限回退"实现原子配额:先 Incr,若超限则 Incr(-n) 退回。
// 高并发下计数瞬时可能越过 limit 但会立即回退,最终不会真正记入超限的量。
func (c *Counter) Allow(key string, n, limit int64) bool {
	now := time.Now().UnixNano()
	if c.store != nil {
		sk := c.storeKey(key, now)
		v, err := c.store.Incr(context.Background(), sk, n, c.window)
		if err != nil {
			c.reportErr("incr", key, err)
			return true // fail-open:存储故障不误伤正常请求
		}
		if v > limit {
			if _, err := c.store.Incr(context.Background(), sk, -n, c.window); err != nil {
				c.reportErr("incr-rollback", key, err)
			}
			return false
		}
		return true
	}
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

// Reset 清零 key 的计数(删除其状态)。store 模式删除当前窗口的计数键。
func (c *Counter) Reset(key string) {
	if c.store != nil {
		if err := c.store.Delete(context.Background(), c.storeKey(key, time.Now().UnixNano())); err != nil {
			c.reportErr("delete", key, err)
		}
		return
	}
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
