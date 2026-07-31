package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Store persists cached response bytes. Implementations must be concurrent-safe.
type Store interface {
	Get(key string) ([]byte, bool)
	Set(key string, data []byte, ttl time.Duration)
	Delete(key string)
}

// ---------- Options ----------

// Option configures the cache interceptor.
type Option func(*config)

type config struct {
	defaultTTL   time.Duration
	methods      map[string]bool
	filter       func(ctx context.Context, method string, req any) bool
	keyFn        func(method string, req any) string
	singleflight bool
}

const defaultTTL = time.Minute

// WithDefaultTTL sets the cache TTL (default 1 min).
func WithDefaultTTL(d time.Duration) Option {
	return func(c *config) { c.defaultTTL = d }
}

// WithMethods restricts caching to the listed full method names
// (e.g. "/pkg.Service/Method"). Nil or empty = cache all unary methods.
func WithMethods(methods ...string) Option {
	return func(c *config) {
		c.methods = make(map[string]bool, len(methods))
		for _, m := range methods {
			c.methods[m] = true
		}
	}
}

// WithFilter adds a custom predicate checked before cache lookup.
// Return true = eligible; false = bypass cache.
func WithFilter(fn func(ctx context.Context, method string, req any) bool) Option {
	return func(c *config) { c.filter = fn }
}

// WithKeyFunc overrides the default cache key generation
// (method + SHA-256 of proto-marshalled request).
func WithKeyFunc(fn func(method string, req any) string) Option {
	return func(c *config) { c.keyFn = fn }
}

// WithSingleFlight deduplicates concurrent cache-miss invocations for the
// same key so all concurrent callers share the result of a single RPC.
func WithSingleFlight() Option {
	return func(c *config) { c.singleflight = true }
}

// ---------- CacheInterceptor ----------

// CacheInterceptor caches gRPC unary responses.
// Create with NewCacheInterceptor, then pass UnaryClientInterceptor() to
// grpc.WithChainUnaryInterceptor.
type CacheInterceptor struct {
	store  Store
	cfg    config
	hits   int64
	misses int64

	mu       sync.Mutex
	inflight map[string]*inflightCall
}

type inflightCall struct {
	done chan struct{}
	data []byte
	err  error
}

// Stats holds hit/miss counters.
type Stats struct {
	Hits   int64
	Misses int64
}

// NewCacheInterceptor creates a cache interceptor with the given store and options.
func NewCacheInterceptor(store Store, opts ...Option) *CacheInterceptor {
	cfg := config{defaultTTL: defaultTTL}
	for _, o := range opts {
		o(&cfg)
	}
	return &CacheInterceptor{
		store:    store,
		cfg:      cfg,
		inflight: make(map[string]*inflightCall),
	}
}

// Stats returns cache hit/miss counters (atomic reads).
func (c *CacheInterceptor) Stats() Stats {
	return Stats{
		Hits:   atomic.LoadInt64(&c.hits),
		Misses: atomic.LoadInt64(&c.misses),
	}
}

// UnaryClientInterceptor returns the interceptor for use with
// grpc.WithChainUnaryInterceptor.
func (c *CacheInterceptor) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return c.intercept
}

// UnaryClientInterceptor is a convenience that creates a CacheInterceptor
// internally and returns the interceptor function directly.
func UnaryClientInterceptor(store Store, opts ...Option) grpc.UnaryClientInterceptor {
	return NewCacheInterceptor(store, opts...).intercept
}

func (c *CacheInterceptor) intercept(
	ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
) error {
	if !c.cacheable(ctx, method, req) {
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	key, err := c.key(method, req)
	if err != nil {
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	// Cache lookup
	if data, ok := c.store.Get(key); ok {
		replyMsg, ok := reply.(proto.Message)
		if !ok {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		proto.Reset(replyMsg)
		if err := proto.Unmarshal(data, replyMsg); err == nil {
			atomic.AddInt64(&c.hits, 1)
			return nil
		}
	}

	// Cache miss
	atomic.AddInt64(&c.misses, 1)

	if c.cfg.singleflight {
		return c.invokeSingleFlight(ctx, method, req, reply, cc, invoker, key, opts)
	}
	return c.invokeAndStore(ctx, method, req, reply, cc, invoker, key, opts)
}

// ---------- internals ----------

func (c *CacheInterceptor) cacheable(ctx context.Context, method string, req any) bool {
	if len(c.cfg.methods) > 0 && !c.cfg.methods[method] {
		return false
	}
	if c.cfg.filter != nil && !c.cfg.filter(ctx, method, req) {
		return false
	}
	return true
}

func (c *CacheInterceptor) key(method string, req any) (string, error) {
	if c.cfg.keyFn != nil {
		return c.cfg.keyFn(method, req), nil
	}
	msg, ok := req.(proto.Message)
	if !ok {
		return "", errNotProto
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return method + "|" + hex.EncodeToString(h[:]), nil
}

var errNotProto = &cacheError{"request is not a proto.Message"}

type cacheError struct{ msg string }

func (e *cacheError) Error() string { return e.msg }

func (c *CacheInterceptor) invokeAndStore(
	ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, key string, opts []grpc.CallOption,
) error {
	if err := invoker(ctx, method, req, reply, cc, opts...); err != nil {
		return err
	}
	c.storeReply(key, reply)
	return nil
}

func (c *CacheInterceptor) storeReply(key string, reply any) {
	msg, ok := reply.(proto.Message)
	if !ok {
		return
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		return
	}
	c.store.Set(key, data, c.cfg.defaultTTL)
}

// invokeSingleFlight deduplicates concurrent RPCs for the same key.
// The winner invokes and serialises; waiters deserialise into their own reply.
func (c *CacheInterceptor) invokeSingleFlight(
	ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, key string, opts []grpc.CallOption,
) error {
	c.mu.Lock()
	if e, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-e.done:
			if e.err != nil {
				return e.err
			}
			if replyMsg, ok := reply.(proto.Message); ok && e.data != nil {
				proto.Reset(replyMsg)
				return proto.Unmarshal(e.data, replyMsg)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	e := &inflightCall{done: make(chan struct{})}
	c.inflight[key] = e
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
	}()

	if err := invoker(ctx, method, req, reply, cc, opts...); err != nil {
		e.err = err
		close(e.done)
		return err
	}

	if msg, ok := reply.(proto.Message); ok {
		data, merr := proto.Marshal(msg)
		if merr == nil {
			e.data = data
			c.store.Set(key, data, c.cfg.defaultTTL)
		}
	}

	close(e.done)
	return nil
}

// ---------- MemoryStore ----------

// MemoryStore is a bounded in-memory Store with TTL.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]*storeEntry
	maxSize int
}

type storeEntry struct {
	data      []byte
	expiresAt time.Time
}

// NewMemoryStore creates a memory store. maxSize <= 0 defaults to 256.
func NewMemoryStore(maxSize int) *MemoryStore {
	if maxSize <= 0 {
		maxSize = 256
	}
	return &MemoryStore{
		entries: make(map[string]*storeEntry, maxSize),
		maxSize: maxSize,
	}
}

func (m *MemoryStore) Get(key string) ([]byte, bool) {
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
	return e.data, true
}

func (m *MemoryStore) Set(key string, data []byte, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[key]; !exists && len(m.entries) >= m.maxSize {
		m.evictOne()
	}
	m.entries[key] = &storeEntry{data: data, expiresAt: exp}
}

func (m *MemoryStore) Delete(key string) {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
}

// Len returns the current number of entries.
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

func (m *MemoryStore) evictOne() {
	var oldest string
	var oldestTime time.Time
	for k, e := range m.entries {
		if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
			delete(m.entries, k)
			return
		}
		if oldest == "" || (!e.expiresAt.IsZero() && e.expiresAt.Before(oldestTime)) {
			oldest = k
			oldestTime = e.expiresAt
		}
	}
	if oldest != "" {
		delete(m.entries, oldest)
	}
}

var _ Store = (*MemoryStore)(nil)
