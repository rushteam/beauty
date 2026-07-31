package resty

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPCacheStore persists cached HTTP responses. Implementations must be safe
// for concurrent use.
type HTTPCacheStore interface {
	Get(key string) (*CachedResponse, bool)
	Set(key string, resp *CachedResponse, ttl time.Duration)
	Delete(key string)
}

// CachedResponse is a serializable snapshot of an HTTP response.
type CachedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	StoredAt   time.Time
}

func (cr *CachedResponse) toHTTPResponse(req *http.Request) *http.Response {
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", cr.StatusCode, http.StatusText(cr.StatusCode)),
		StatusCode:    cr.StatusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        cr.Header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(cr.Body)),
		ContentLength: int64(len(cr.Body)),
		Request:       req,
	}
}

// ---------- Options ----------

// CacheTransportOption configures NewCacheTransport.
type CacheTransportOption func(*cacheTransportCfg)

type cacheTransportCfg struct {
	defaultTTL       time.Duration
	forceTTL         bool
	ignoreReqCC      bool
	methods          map[string]bool
	filter           func(*http.Request) bool
	keyFn            func(*http.Request) string
	singleflight     bool
	conditional      bool
}

const defaultCacheTTL = time.Minute

// WithCacheDefaultTTL sets the fallback TTL used when the response carries
// no Cache-Control max-age directive (default 1 min).
func WithCacheDefaultTTL(d time.Duration) CacheTransportOption {
	return func(c *cacheTransportCfg) { c.defaultTTL = d }
}

// WithCacheForceTTL ignores both request-side and response-side Cache-Control
// directives, always using the configured default TTL. Useful for APIs that
// don't set cache headers or when you want full control over TTL.
func WithCacheForceTTL() CacheTransportOption {
	return func(c *cacheTransportCfg) { c.forceTTL = true }
}

// WithCacheIgnoreRequestDirectives ignores request-side Cache-Control
// (no-store, no-cache) while still respecting the server's response directives.
// Useful when upstream callers set no-cache/no-store but you still want
// transport-level caching controlled by the server's max-age / Expires.
func WithCacheIgnoreRequestDirectives() CacheTransportOption {
	return func(c *cacheTransportCfg) { c.ignoreReqCC = true }
}

// WithCacheMethods specifies which HTTP methods are cacheable (default: GET).
func WithCacheMethods(methods ...string) CacheTransportOption {
	return func(c *cacheTransportCfg) {
		c.methods = make(map[string]bool, len(methods))
		for _, m := range methods {
			c.methods[strings.ToUpper(m)] = true
		}
	}
}

// WithCacheFilter adds a custom predicate checked after the method filter.
// Return true = eligible for caching; false = bypass cache entirely.
func WithCacheFilter(fn func(*http.Request) bool) CacheTransportOption {
	return func(c *cacheTransportCfg) { c.filter = fn }
}

// WithCacheKeyFunc overrides the default cache key generation (method + URL).
func WithCacheKeyFunc(fn func(*http.Request) string) CacheTransportOption {
	return func(c *cacheTransportCfg) { c.keyFn = fn }
}

// WithCacheSingleFlight deduplicates concurrent cache-miss fetches for the
// same key so all concurrent callers share the result of a single round-trip.
func WithCacheSingleFlight() CacheTransportOption {
	return func(c *cacheTransportCfg) { c.singleflight = true }
}

// WithCacheConditionalRequest enables conditional revalidation: when a cached
// entry expires the transport sends If-None-Match / If-Modified-Since and
// handles 304 Not Modified by refreshing the cache.
func WithCacheConditionalRequest() CacheTransportOption {
	return func(c *cacheTransportCfg) { c.conditional = true }
}

// ---------- CacheTransport ----------

// CacheTransport is an http.RoundTripper that caches responses.
//
// Only safe (GET by default) requests with cacheable responses are stored.
// The transport respects standard Cache-Control directives (no-store, no-cache,
// max-age, private) unless WithCacheForceTTL is set.
type CacheTransport struct {
	next  http.RoundTripper
	store HTTPCacheStore
	cfg   cacheTransportCfg
	hits  int64
	misses int64

	mu       sync.Mutex
	inflight map[string]*inflightCall
}

type inflightCall struct {
	done chan struct{}
	cr   *CachedResponse
	err  error
}

// CacheTransportStats holds hit/miss counters.
type CacheTransportStats struct {
	Hits   int64
	Misses int64
}

// NewCacheTransport wraps next with an HTTP response cache.
// If next is nil, http.DefaultTransport is used.
func NewCacheTransport(next http.RoundTripper, store HTTPCacheStore, opts ...CacheTransportOption) *CacheTransport {
	if next == nil {
		next = http.DefaultTransport
	}
	cfg := cacheTransportCfg{
		defaultTTL: defaultCacheTTL,
		methods:    map[string]bool{http.MethodGet: true},
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &CacheTransport{
		next:     next,
		store:    store,
		cfg:      cfg,
		inflight: make(map[string]*inflightCall),
	}
}

// Stats returns cache hit/miss counters (atomic reads).
func (t *CacheTransport) Stats() CacheTransportStats {
	return CacheTransportStats{
		Hits:   atomic.LoadInt64(&t.hits),
		Misses: atomic.LoadInt64(&t.misses),
	}
}

// RoundTrip implements http.RoundTripper.
func (t *CacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.cacheable(req) {
		return t.next.RoundTrip(req)
	}

	if !t.cfg.forceTTL && !t.cfg.ignoreReqCC {
		if parseCacheControl(req.Header.Get("Cache-Control")).noStore {
			return t.next.RoundTrip(req)
		}
	}

	key := t.key(req)

	if cached, ok := t.store.Get(key); ok {
		ttl := t.effectiveTTL(cached)
		age := time.Since(cached.StoredAt)
		fresh := ttl > 0 && age < ttl
		mustRevalidate := !t.cfg.forceTTL && !t.cfg.ignoreReqCC &&
			parseCacheControl(req.Header.Get("Cache-Control")).noCache

		if fresh && !mustRevalidate {
			atomic.AddInt64(&t.hits, 1)
			return cached.toHTTPResponse(req), nil
		}

		if t.cfg.conditional {
			return t.conditionalFetch(req, key, cached)
		}
	}

	atomic.AddInt64(&t.misses, 1)
	return t.fetchAndStore(req, key)
}

// ---------- internals ----------

func (t *CacheTransport) cacheable(req *http.Request) bool {
	if !t.cfg.methods[req.Method] {
		return false
	}
	if t.cfg.filter != nil && !t.cfg.filter(req) {
		return false
	}
	return true
}

func (t *CacheTransport) key(req *http.Request) string {
	if t.cfg.keyFn != nil {
		return t.cfg.keyFn(req)
	}
	return req.Method + " " + req.URL.String()
}

func (t *CacheTransport) effectiveTTL(cr *CachedResponse) time.Duration {
	if t.cfg.forceTTL {
		return t.cfg.defaultTTL
	}
	cc := parseCacheControl(cr.Header.Get("Cache-Control"))
	if cc.noStore {
		return 0
	}
	if cc.maxAge >= 0 {
		return time.Duration(cc.maxAge) * time.Second
	}
	if exp := cr.Header.Get("Expires"); exp != "" {
		if et, err := http.ParseTime(exp); err == nil {
			if d := time.Until(et); d > 0 {
				return d
			}
			return 0
		}
	}
	return t.cfg.defaultTTL
}

func (t *CacheTransport) storable(resp *http.Response) bool {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
	case resp.StatusCode == http.StatusMovedPermanently,
		resp.StatusCode == http.StatusPermanentRedirect:
	default:
		return false
	}
	if !t.cfg.forceTTL {
		cc := parseCacheControl(resp.Header.Get("Cache-Control"))
		if cc.noStore || cc.private {
			return false
		}
	}
	if resp.Header.Get("Vary") == "*" {
		return false
	}
	return true
}

// conditionalFetch sends a conditional request (If-None-Match / If-Modified-Since).
// On 304 it refreshes the cache and returns the stored body.
func (t *CacheTransport) conditionalFetch(req *http.Request, key string, cached *CachedResponse) (*http.Response, error) {
	condReq := req.Clone(req.Context())
	hasValidator := false
	if etag := cached.Header.Get("ETag"); etag != "" {
		condReq.Header.Set("If-None-Match", etag)
		hasValidator = true
	}
	if lm := cached.Header.Get("Last-Modified"); lm != "" {
		condReq.Header.Set("If-Modified-Since", lm)
		hasValidator = true
	}
	if !hasValidator {
		atomic.AddInt64(&t.misses, 1)
		return t.fetchAndStore(req, key)
	}

	resp, err := t.next.RoundTrip(condReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotModified {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		updated := &CachedResponse{
			StatusCode: cached.StatusCode,
			Header:     cached.Header.Clone(),
			Body:       cached.Body,
			StoredAt:   time.Now(),
		}
		for k, v := range resp.Header {
			updated.Header[k] = v
		}
		t.store.Set(key, updated, t.effectiveTTL(updated))
		atomic.AddInt64(&t.hits, 1)
		return updated.toHTTPResponse(req), nil
	}
	atomic.AddInt64(&t.misses, 1)
	return t.storeResponse(key, req, resp)
}

func (t *CacheTransport) fetchAndStore(req *http.Request, key string) (*http.Response, error) {
	if t.cfg.singleflight {
		return t.fetchSingleFlight(req, key)
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	return t.storeResponse(key, req, resp)
}

// fetchSingleFlight deduplicates concurrent fetches for the same key.
// The first goroutine performs the actual round-trip; others wait and receive
// an independent copy of the response.
func (t *CacheTransport) fetchSingleFlight(req *http.Request, key string) (*http.Response, error) {
	t.mu.Lock()
	if e, ok := t.inflight[key]; ok {
		t.mu.Unlock()
		select {
		case <-e.done:
			if e.err != nil {
				return nil, e.err
			}
			return e.cr.toHTTPResponse(req), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	e := &inflightCall{done: make(chan struct{})}
	t.inflight[key] = e
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.inflight, key)
		t.mu.Unlock()
	}()

	resp, err := t.next.RoundTrip(req)
	if err != nil {
		e.err = err
		close(e.done)
		return nil, err
	}

	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		e.err = readErr
		close(e.done)
		return nil, readErr
	}

	cr := &CachedResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
		StoredAt:   time.Now(),
	}
	if t.storable(resp) {
		if ttl := t.effectiveTTL(cr); ttl > 0 {
			t.store.Set(key, cr, ttl)
		}
	}

	e.cr = cr
	close(e.done)
	return cr.toHTTPResponse(req), nil
}

// storeResponse buffers and caches a storable response; non-storable responses
// pass through with an untouched body.
func (t *CacheTransport) storeResponse(key string, req *http.Request, resp *http.Response) (*http.Response, error) {
	if !t.storable(resp) {
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	cr := &CachedResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
		StoredAt:   time.Now(),
	}
	if ttl := t.effectiveTTL(cr); ttl > 0 {
		t.store.Set(key, cr, ttl)
	}
	return cr.toHTTPResponse(req), nil
}

// ---------- Cache-Control parsing ----------

type cacheDirectives struct {
	noStore   bool
	noCache   bool
	private   bool
	public    bool
	maxAge    int // -1 = not present
	mustReval bool
}

func parseCacheControl(header string) cacheDirectives {
	d := cacheDirectives{maxAge: -1}
	if header == "" {
		return d
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		switch {
		case lower == "no-store":
			d.noStore = true
		case lower == "no-cache":
			d.noCache = true
		case lower == "private":
			d.private = true
		case lower == "public":
			d.public = true
		case lower == "must-revalidate":
			d.mustReval = true
		case strings.HasPrefix(lower, "max-age="):
			if v, err := strconv.Atoi(strings.TrimPrefix(lower, "max-age=")); err == nil {
				d.maxAge = v
			}
		}
	}
	return d
}

// ---------- MemoryHTTPCache ----------

// MemoryHTTPCache is a bounded in-memory HTTPCacheStore. It evicts the earliest-expiring
// entry when full, and lazily removes expired entries on access.
// Suitable for development, testing, and single-instance deployments; production can
// implement HTTPCacheStore backed by Redis / Memcached.
type MemoryHTTPCache struct {
	mu      sync.RWMutex
	entries map[string]*httpCacheEntry
	maxSize int
}

type httpCacheEntry struct {
	resp      *CachedResponse
	expiresAt time.Time
}

// NewMemoryHTTPCache creates a memory cache. maxSize <= 0 defaults to 256.
func NewMemoryHTTPCache(maxSize int) *MemoryHTTPCache {
	if maxSize <= 0 {
		maxSize = 256
	}
	return &MemoryHTTPCache{
		entries: make(map[string]*httpCacheEntry, maxSize),
		maxSize: maxSize,
	}
}

func (m *MemoryHTTPCache) Get(key string) (*CachedResponse, bool) {
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

func (m *MemoryHTTPCache) Set(key string, resp *CachedResponse, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[key]; !exists && len(m.entries) >= m.maxSize {
		m.evictOne()
	}
	m.entries[key] = &httpCacheEntry{resp: resp, expiresAt: exp}
}

func (m *MemoryHTTPCache) Delete(key string) {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
}

// Len returns the current number of entries.
func (m *MemoryHTTPCache) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

func (m *MemoryHTTPCache) evictOne() {
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

var _ HTTPCacheStore = (*MemoryHTTPCache)(nil)
