// Package cooldown 提供按 key 的冷却(CD)原语:某个动作触发后,要等到冷却结束
// 才能再次触发。纯内存、并发安全。
//
// 与相邻限流原语的区别:
//   - ratelimit 控"速率"(每秒 N 次,令牌桶);
//   - counter 控"窗口内累计次数"(每分钟 ≤ N);
//   - cooldown 控"两次动作的最小间隔 / 下次可用时刻",per-(key) 维度。
//     典型:技能 CD(放完技能 8s 后才能再放)、每日签到(领了要到次日)、
//     发言间隔(发一条后 3s 才能再发)、按钮防连点 / 二次确认窗口。
//
// 核心是"下次可用时刻"的时间戳:Trigger 记录 now+cd;Ready 判断 now 是否已过;
// Remaining 返回还剩多久。TryTrigger 是"检查 + 触发"的原子组合(未在 CD 中则
// 触发并返回 true,否则返回 false),避免检查与触发之间的竞态。
//
// 支持默认 CD(New 时设定)与 per-call CD(TriggerFor,不同动作不同冷却)。
// 分片锁降低争用;空闲 key 由后台 gc 回收。零值不可用,用 New 构造;Stop 后 gc 退出。
//
// 生产多实例:默认内存实现的冷却不跨实例(各台各算各的,换实例可绕过 CD)。用
// WithStore 接入 kvstore.Store(如 Redis)后,冷却跨实例一致——"冷却中"用一个 TTL
// 键表示(键在=冷却中,TTL=剩余时间),TryTrigger 用 SetNX 原子抢占。
package cooldown

import (
	"context"
	"hash/maphash"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/kvstore"
)

const shardCount = 16

type shard struct {
	mu    sync.Mutex
	until map[string]int64 // key → 下次可用的 unix nano
}

// config 配置。
type config struct {
	defaultCD  time.Duration
	gcInterval time.Duration
	store      kvstore.Store
	onStoreErr func(op, key string, err error)
}

// Option 配置 Cooldown。
type Option func(*config)

// WithGCInterval 设置空闲 key 清扫间隔(默认 1 分钟)。
func WithGCInterval(d time.Duration) Option { return func(c *config) { c.gcInterval = d } }

// WithStore 让冷却走外部共享存储(如 Redis),使冷却跨实例一致。配置后所有操作
// 路由到 store,不再使用内存分片与 gc。
func WithStore(s kvstore.Store) Option { return func(c *config) { c.store = s } }

// WithOnStoreError 设置 store 出错回调(网络故障等)。默认静默;出错时 Ready 返回
// true、TryTrigger 放行(fail-open),由此回调上报供监控。
func WithOnStoreError(fn func(op, key string, err error)) Option {
	return func(c *config) { c.onStoreErr = fn }
}

// Cooldown 按 key 的冷却管理器。零值不可用,用 New 构造。并发安全。
type Cooldown struct {
	defaultCD  int64 // ns
	gcInterval time.Duration
	store      kvstore.Store
	onStoreErr func(op, key string, err error)
	seed       maphash.Seed
	shards     [shardCount]*shard
	stopCh     chan struct{}
	stop       sync.Once
}

// New 创建冷却管理器。defaultCD 为默认冷却时长(可被 TriggerFor / ReadyFor 覆盖)。
func New(defaultCD time.Duration, opts ...Option) *Cooldown {
	cfg := config{defaultCD: defaultCD, gcInterval: time.Minute}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.gcInterval <= 0 {
		cfg.gcInterval = time.Minute
	}
	c := &Cooldown{
		defaultCD:  int64(defaultCD),
		gcInterval: cfg.gcInterval,
		store:      cfg.store,
		onStoreErr: cfg.onStoreErr,
		seed:       maphash.MakeSeed(),
		stopCh:     make(chan struct{}),
	}
	if c.store == nil {
		for i := range c.shards {
			c.shards[i] = &shard{until: make(map[string]int64)}
		}
		go c.gc()
	}
	return c
}

func (c *Cooldown) shardFor(key string) *shard {
	return c.shards[maphash.String(c.seed, key)&(shardCount-1)]
}

func (c *Cooldown) storeKey(key string) string { return "cd:" + key }

func (c *Cooldown) reportErr(op, key string, err error) {
	if err != nil && c.onStoreErr != nil {
		c.onStoreErr(op, key, err)
	}
}

// Ready 判断 key 是否已冷却完毕(可再次触发)。不在记录中视为就绪。
func (c *Cooldown) Ready(key string) bool {
	if c.store != nil {
		_, ok, err := c.store.TTL(context.Background(), c.storeKey(key))
		if err != nil {
			c.reportErr("ttl", key, err)
			return true // fail-open
		}
		return !ok // 键存在=冷却中
	}
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().UnixNano() >= s.until[key]
}

// Remaining 返回 key 剩余冷却时长;已就绪返回 0。
func (c *Cooldown) Remaining(key string) time.Duration {
	if c.store != nil {
		ttl, ok, err := c.store.TTL(context.Background(), c.storeKey(key))
		if err != nil {
			c.reportErr("ttl", key, err)
			return 0
		}
		if !ok || ttl < 0 {
			return 0
		}
		return ttl
	}
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	rem := s.until[key] - time.Now().UnixNano()
	if rem <= 0 {
		return 0
	}
	return time.Duration(rem)
}

// Trigger 用默认 CD 触发 key 的冷却(无条件把下次可用时刻设为 now+defaultCD)。
func (c *Cooldown) Trigger(key string) {
	c.TriggerFor(key, time.Duration(c.defaultCD))
}

// TriggerFor 用指定 cd 触发冷却(不同动作不同冷却时用)。cd<=0 视为立即就绪。
func (c *Cooldown) TriggerFor(key string, cd time.Duration) {
	if c.store != nil {
		if cd <= 0 {
			c.Reset(key)
			return
		}
		// 无条件覆盖:写一个 TTL=cd 的键。
		if err := c.store.Set(context.Background(), c.storeKey(key), []byte{1}, cd); err != nil {
			c.reportErr("set", key, err)
		}
		return
	}
	until := time.Now().Add(cd).UnixNano()
	s := c.shardFor(key)
	s.mu.Lock()
	s.until[key] = until
	s.mu.Unlock()
}

// TryTrigger 原子地"检查 + 触发":若 key 已就绪,则触发默认 CD 并返回 true;
// 否则不改动、返回 false。用于"能放技能就放并进 CD"这类竞态敏感场景。
func (c *Cooldown) TryTrigger(key string) bool {
	return c.TryTriggerFor(key, time.Duration(c.defaultCD))
}

// TryTriggerFor 同 TryTrigger,但用指定 cd。
func (c *Cooldown) TryTriggerFor(key string, cd time.Duration) bool {
	if c.store != nil {
		if cd <= 0 {
			return true // 零 CD 恒就绪
		}
		// SetNX 原子抢占:只有键不存在(就绪)时才写入成功。
		ok, err := c.store.SetNX(context.Background(), c.storeKey(key), []byte{1}, cd)
		if err != nil {
			c.reportErr("setnx", key, err)
			return true // fail-open
		}
		return ok
	}
	now := time.Now().UnixNano()
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.until[key] {
		return false // 仍在 CD 中
	}
	s.until[key] = time.Now().Add(cd).UnixNano()
	return true
}

// Reset 清除 key 的冷却(立即就绪)。
func (c *Cooldown) Reset(key string) {
	if c.store != nil {
		if err := c.store.Delete(context.Background(), c.storeKey(key)); err != nil {
			c.reportErr("delete", key, err)
		}
		return
	}
	s := c.shardFor(key)
	s.mu.Lock()
	delete(s.until, key)
	s.mu.Unlock()
}

// Stop 停止 gc goroutine。幂等。
func (c *Cooldown) Stop() {
	c.stop.Do(func() { close(c.stopCh) })
}

// gc 周期清理已过期(早已就绪)的 key,回收内存。
func (c *Cooldown) gc() {
	ticker := time.NewTicker(c.gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().UnixNano()
			for _, s := range c.shards {
				s.mu.Lock()
				for k, until := range s.until {
					if now >= until {
						delete(s.until, k)
					}
				}
				s.mu.Unlock()
			}
		case <-c.stopCh:
			return
		}
	}
}
