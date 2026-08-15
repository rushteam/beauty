package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"iter"
	"sync"
	"sync/atomic"
	"time"
)

// CacheStore 是 LLM 响应缓存的存储后端。实现应并发安全。
// Get 未命中返回 (nil, false);Set 的 ttl<=0 表示永不过期。
type CacheStore interface {
	Get(ctx context.Context, key string) (*Response, bool)
	Set(ctx context.Context, key string, resp *Response, ttl time.Duration)
}

// CacheOption 配置 Cache 中间件。
type CacheOption func(*cacheConfig)

type cacheConfig struct {
	ttl    time.Duration
	filter func(Request) bool
	keyFn  func(Request) string
}

// WithCacheTTL 设置缓存有效期(默认 1 小时)。<=0 表示永不过期。
func WithCacheTTL(d time.Duration) CacheOption {
	return func(c *cacheConfig) { c.ttl = d }
}

// WithCacheFilter 控制哪些请求走缓存。返回 true 时缓存/查缓存;false 跳过。
// 默认:仅 temperature=0 时缓存(非零温度回复不确定,缓存无意义)。
func WithCacheFilter(fn func(Request) bool) CacheOption {
	return func(c *cacheConfig) { c.filter = fn }
}

// WithCacheKey 自定义 cache key 生成(默认基于请求关键字段的 SHA-256)。
func WithCacheKey(fn func(Request) string) CacheOption {
	return func(c *cacheConfig) { c.keyFn = fn }
}

// CacheStats 是缓存命中统计。
type CacheStats struct {
	Hits   int64
	Misses int64
}

// CacheClient 是带响应缓存的 Client 包装。
type CacheClient struct {
	c    Client
	cfg  cacheConfig
	s    CacheStore
	hits int64
	miss int64
}

// Cache 为 Client 加响应缓存。相同请求(model/messages/tools/system/temperature/response_format)
// 返回缓存的 Response,避免重复调用 API(省钱/降延迟)。
//
// Stream 场景:缓存命中时回放为若干 Chunk(Delta=全文内容 + ToolCalls + Usage);
// 未命中时正常流式,迭代结束后写入缓存。
//
// 默认仅缓存 temperature=0 的请求(确定性回复);自定义用 WithCacheFilter。
func Cache(c Client, store CacheStore, opts ...CacheOption) *CacheClient {
	cfg := cacheConfig{ttl: time.Hour}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.filter == nil {
		cfg.filter = func(r Request) bool { return r.Temperature == 0 }
	}
	if cfg.keyFn == nil {
		cfg.keyFn = defaultCacheKey
	}
	return &CacheClient{c: c, cfg: cfg, s: store}
}

// Stats 返回命中/未命中计数。
func (cc *CacheClient) Stats() CacheStats {
	return CacheStats{
		Hits:   atomic.LoadInt64(&cc.hits),
		Misses: atomic.LoadInt64(&cc.miss),
	}
}

func (cc *CacheClient) Generate(ctx context.Context, req Request) (*Response, error) {
	if !cc.cfg.filter(req) {
		return cc.c.Generate(ctx, req)
	}
	key := cc.cfg.keyFn(req)
	if resp, ok := cc.s.Get(ctx, key); ok {
		atomic.AddInt64(&cc.hits, 1)
		cp := *resp
		return &cp, nil
	}
	atomic.AddInt64(&cc.miss, 1)
	resp, err := cc.c.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	cc.s.Set(ctx, key, resp, cc.cfg.ttl)
	return resp, nil
}

func (cc *CacheClient) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		if !cc.cfg.filter(req) {
			for chunk, err := range cc.c.Stream(ctx, req) {
				if !yield(chunk, err) {
					return
				}
				if err != nil {
					return
				}
			}
			return
		}
		key := cc.cfg.keyFn(req)
		if resp, ok := cc.s.Get(ctx, key); ok {
			atomic.AddInt64(&cc.hits, 1)
			cp := *resp
			if cp.Thinking != "" {
				if !yield(Chunk{ThinkingDelta: cp.Thinking}, nil) {
					return
				}
			}
			if cp.Content != "" {
				if !yield(Chunk{Delta: cp.Content}, nil) {
					return
				}
			}
			yield(Chunk{ToolCalls: cp.ToolCalls, Usage: &cp.Usage, Thinking: cp.Thinking}, nil)
			return
		}
		atomic.AddInt64(&cc.miss, 1)

		var assembled Response
		assembled.Model = req.Model
		for chunk, err := range cc.c.Stream(ctx, req) {
			if err != nil {
				yield(chunk, err)
				return
			}
			if chunk.Delta != "" {
				assembled.Content += chunk.Delta
			}
			if chunk.ThinkingDelta != "" {
				assembled.Thinking += chunk.ThinkingDelta
			}
			if len(chunk.ToolCalls) > 0 {
				assembled.ToolCalls = chunk.ToolCalls
			}
			if chunk.Usage != nil {
				assembled.Usage = *chunk.Usage
			}
			if !yield(chunk, nil) {
				return
			}
		}
		cc.s.Set(ctx, key, &assembled, cc.cfg.ttl)
	}
}

func defaultCacheKey(req Request) string {
	type keyData struct {
		Model          string          `json:"m"`
		System         string          `json:"s,omitempty"`
		Messages       []Message       `json:"msg"`
		Tools          []ToolDef       `json:"t,omitempty"`
		ToolChoice     string          `json:"tc,omitempty"`
		ResponseFormat *ResponseFormat `json:"rf,omitempty"`
		Temperature    float64         `json:"temp,omitempty"`
	}
	b, _ := json.Marshal(keyData{
		Model: req.Model, System: req.System, Messages: req.Messages,
		Tools: req.Tools, ToolChoice: req.ToolChoice,
		ResponseFormat: req.ResponseFormat, Temperature: req.Temperature,
	})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ---- 内置 MemoryCacheStore ----

// MemoryCacheStore 是并发安全的内存缓存(LRU 淘汰 + TTL)。适合开发/测试/单机;
// 生产可实现 CacheStore 接口接 Redis 等外部存储。
type MemoryCacheStore struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	maxSize int
}

type cacheEntry struct {
	resp      *Response
	expiresAt time.Time
}

// NewMemoryCacheStore 创建内存缓存。maxSize<=0 时用 1024。
func NewMemoryCacheStore(maxSize int) *MemoryCacheStore {
	if maxSize <= 0 {
		maxSize = 1024
	}
	return &MemoryCacheStore{entries: make(map[string]*cacheEntry, maxSize), maxSize: maxSize}
}

func (m *MemoryCacheStore) Get(_ context.Context, key string) (*Response, bool) {
	m.mu.RLock()
	e, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		m.mu.Lock()
		delete(m.entries, key)
		m.mu.Unlock()
		return nil, false
	}
	return e.resp, true
}

func (m *MemoryCacheStore) Set(_ context.Context, key string, resp *Response, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) >= m.maxSize {
		m.evict()
	}
	m.entries[key] = &cacheEntry{resp: resp, expiresAt: exp}
}

// Len 返回当前缓存条目数。
func (m *MemoryCacheStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// evict 删除最早过期的条目;都未过期则随机删一个(近似 LRU)。需持有写锁。
func (m *MemoryCacheStore) evict() {
	var oldest string
	var oldestTime time.Time
	for k, e := range m.entries {
		if oldest == "" || (!e.expiresAt.IsZero() && e.expiresAt.Before(oldestTime)) {
			oldest = k
			oldestTime = e.expiresAt
		}
	}
	if oldest != "" {
		delete(m.entries, oldest)
	}
}

var _ CacheStore = (*MemoryCacheStore)(nil)
