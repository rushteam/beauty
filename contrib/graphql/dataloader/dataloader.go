// Package dataloader 提供泛型 DataLoader 实现,用于解决 GraphQL resolver 中的
// N+1 查询问题。每请求生命周期缓存 + 批量合并,通过 HTTP 中间件自动注入 context。
package dataloader

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// BatchFunc 是批量加载函数。接收一批 keys,返回 key→value 映射。
// 调用者保证 keys 去重且非空。
type BatchFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

// Loader 是泛型 DataLoader。并发安全,按请求生命周期缓存结果。
type Loader[K comparable, V any] struct {
	batchFn   BatchFunc[K, V]
	batchSize int
	batchWait time.Duration
	useCache  bool

	mu      sync.Mutex
	cache   map[K]*result[V]
	pending []request[K, V]
	timer   *time.Timer
}

type result[V any] struct {
	value V
	err   error
}

type request[K comparable, V any] struct {
	key K
	ch  chan *result[V]
}

// LoaderOption 配置 Loader。
type LoaderOption func(*loaderConfig)

type loaderConfig struct {
	batchSize int
	batchWait time.Duration
	cache     bool
}

// WithBatchSize 设置单批最大 key 数(默认 100)。
func WithBatchSize(n int) LoaderOption {
	return func(c *loaderConfig) { c.batchSize = n }
}

// WithBatchWait 设置收集窗口(默认 2ms)。
func WithBatchWait(d time.Duration) LoaderOption {
	return func(c *loaderConfig) { c.batchWait = d }
}

// WithCache 启用/禁用请求内缓存(默认 true)。
func WithCache(enabled bool) LoaderOption {
	return func(c *loaderConfig) { c.cache = enabled }
}

// NewLoader 创建 DataLoader。batchFn 在收集窗口结束或达到 batchSize 时被调用。
func NewLoader[K comparable, V any](batchFn BatchFunc[K, V], opts ...LoaderOption) *Loader[K, V] {
	cfg := loaderConfig{batchSize: 100, batchWait: 2 * time.Millisecond, cache: true}
	for _, o := range opts {
		o(&cfg)
	}
	return &Loader[K, V]{
		batchFn:   batchFn,
		batchSize: cfg.batchSize,
		batchWait: cfg.batchWait,
		useCache:  cfg.cache,
		cache:     make(map[K]*result[V]),
	}
}

// Load 加载单个 key。相同 key 在同一请求内只会批量加载一次(如启用缓存)。
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	l.mu.Lock()

	if l.useCache {
		if r, ok := l.cache[key]; ok {
			l.mu.Unlock()
			return r.value, r.err
		}
	}

	ch := make(chan *result[V], 1)
	l.pending = append(l.pending, request[K, V]{key: key, ch: ch})

	if len(l.pending) >= l.batchSize {
		l.dispatchLocked(ctx)
	} else if l.timer == nil {
		l.timer = time.AfterFunc(l.batchWait, func() {
			l.mu.Lock()
			l.dispatchLocked(ctx)
			l.mu.Unlock()
		})
	}
	l.mu.Unlock()

	select {
	case r := <-ch:
		return r.value, r.err
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
}

// LoadMany 批量加载多个 key。
func (l *Loader[K, V]) LoadMany(ctx context.Context, keys []K) ([]V, error) {
	results := make([]V, len(keys))
	for i, k := range keys {
		v, err := l.Load(ctx, k)
		if err != nil {
			return nil, err
		}
		results[i] = v
	}
	return results, nil
}

// Prime 手动填充缓存(用于从已知数据预热)。
func (l *Loader[K, V]) Prime(key K, value V) {
	if !l.useCache {
		return
	}
	l.mu.Lock()
	l.cache[key] = &result[V]{value: value}
	l.mu.Unlock()
}

func (l *Loader[K, V]) dispatchLocked(ctx context.Context) {
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}

	batch := l.pending
	l.pending = nil

	if len(batch) == 0 {
		return
	}

	go func() {
		keys := make([]K, 0, len(batch))
		seen := make(map[K]struct{}, len(batch))
		for _, r := range batch {
			if _, ok := seen[r.key]; !ok {
				keys = append(keys, r.key)
				seen[r.key] = struct{}{}
			}
		}

		values, err := l.batchFn(ctx, keys)

		l.mu.Lock()
		for _, r := range batch {
			var res *result[V]
			if err != nil {
				res = &result[V]{err: err}
			} else if v, ok := values[r.key]; ok {
				res = &result[V]{value: v}
			} else {
				var zero V
				res = &result[V]{value: zero}
			}
			if l.useCache {
				l.cache[r.key] = res
			}
			r.ch <- res
		}
		l.mu.Unlock()
	}()
}

// ===== HTTP Middleware for per-request loaders =====

type contextKey struct{}

// Registry 持有一组 DataLoader 实例(每请求一份)。
type Registry struct {
	loaders sync.Map
}

// Register 注册一个 loader 到 registry。
func Register[K comparable, V any](reg *Registry, name string, loader *Loader[K, V]) {
	reg.loaders.Store(name, loader)
}

// Get 从 context 中取出 registry 并获取指定 loader。
func Get[K comparable, V any](ctx context.Context, name string) (*Loader[K, V], bool) {
	reg, ok := ctx.Value(contextKey{}).(*Registry)
	if !ok {
		return nil, false
	}
	v, ok := reg.loaders.Load(name)
	if !ok {
		return nil, false
	}
	loader, ok := v.(*Loader[K, V])
	return loader, ok
}

// Middleware 为每个请求创建新的 DataLoader Registry 并注入 context。
// factory 在每个请求开始时被调用,返回该请求的 Registry。
func Middleware(factory func() *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reg := factory()
			ctx := context.WithValue(r.Context(), contextKey{}, reg)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
