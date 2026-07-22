package cache

import (
	"context"
	"time"

	"github.com/rushteam/beauty/pkg/syncx"
)

// Loader 是防击穿的缓存加载器:在一个有界 Cache 之上叠加 singleflight(同 key 并发回源只打一次)、
// TTL(过期惰性失效)与负缓存(失败也短暂缓存),一站式化解缓存穿透/击穿/雪崩。
//
//   - 穿透:load 失败结果按 negTTL 短暂缓存,挡住对不存在数据的反复回源(negTTL>0 时);
//   - 击穿:同一 key 的并发 miss 经 singleflight 合并,只有一个真正回源,其余等待其结果;
//   - 雪崩:容量有界的 Cache + 各自 TTL,避免同刻大量 key 同时失效冲垮下游(可配合抖动 TTL)。
//
// 键为 string(缓存回源多以 URL/ID 为键);值为泛型 V。并发安全。
type Loader[V any] struct {
	c      Cache[string, item[V]]
	g      syncx.Group[V]
	load   func(ctx context.Context, key string) (V, error)
	ttl    time.Duration
	negTTL time.Duration
	now    func() time.Time
}

type item[V any] struct {
	val V
	err error
	exp time.Time
}

// LoaderOption 配置 Loader。
type LoaderOption func(*loaderCfg)

type loaderCfg struct {
	ttl    time.Duration
	negTTL time.Duration
	now    func() time.Time
}

// WithTTL 设置成功结果的缓存时长(<=0 表示不缓存成功结果,每次都回源)。
func WithTTL(d time.Duration) LoaderOption { return func(c *loaderCfg) { c.ttl = d } }

// WithNegativeTTL 设置失败结果的缓存时长(负缓存;<=0 表示不缓存错误)。
func WithNegativeTTL(d time.Duration) LoaderOption { return func(c *loaderCfg) { c.negTTL = d } }

// WithClock 注入时间源(测试用)。
func WithClock(now func() time.Time) LoaderOption { return func(c *loaderCfg) { c.now = now } }

// NewLoader 用给定的底层缓存 c 与回源函数 load 构造 Loader。
// c 建议用 NewLRU / NewTinyLFU 之一(容量有界)。
func NewLoader[V any](c Cache[string, item[V]], load func(ctx context.Context, key string) (V, error), opts ...LoaderOption) *Loader[V] {
	cfg := loaderCfg{ttl: time.Minute, now: time.Now}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &Loader[V]{c: c, load: load, ttl: cfg.ttl, negTTL: cfg.negTTL, now: cfg.now}
}

// NewLRULoader 便捷构造:LRU(capacity) + Loader。
func NewLRULoader[V any](capacity int, load func(ctx context.Context, key string) (V, error), opts ...LoaderOption) *Loader[V] {
	return NewLoader(NewLRU[string, item[V]](capacity), load, opts...)
}

// Get 返回 key 对应的值:命中未过期缓存直接返回;否则经 singleflight 回源(同 key 并发合并)。
func (l *Loader[V]) Get(ctx context.Context, key string) (V, error) {
	if it, ok := l.c.Get(key); ok && l.now().Before(it.exp) {
		return it.val, it.err
	}
	v, err, _ := l.g.Do(key, func() (V, error) {
		// 进入 singleflight 后再查一次:可能在排队期间已被别的调用填充。
		if it, ok := l.c.Get(key); ok && l.now().Before(it.exp) {
			return it.val, it.err
		}
		v, err := l.load(ctx, key)
		ttl := l.ttl
		if err != nil {
			ttl = l.negTTL // 负缓存
		}
		if ttl > 0 {
			l.c.Set(key, item[V]{val: v, err: err, exp: l.now().Add(ttl)})
		}
		return v, err
	})
	return v, err
}

// Forget 使某个 key 的缓存与在途 singleflight 失效(下次 Get 强制回源)。
func (l *Loader[V]) Forget(key string) {
	l.c.Delete(key)
	l.g.Forget(key)
}
