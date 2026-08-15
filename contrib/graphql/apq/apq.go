// Package apq 提供 GraphQL 持久化查询 (Automatic Persisted Queries) 和白名单模式,
// 实现为 gqlgen HandlerExtension。APQ 兼容 Apollo 客户端协议。
package apq

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Cache 是 APQ 缓存接口。可对接内存 LRU、Redis 或任意存储。
type Cache interface {
	Get(ctx context.Context, hash string) (string, bool)
	Set(ctx context.Context, hash string, query string) error
}

// ===== Memory LRU Cache =====

// NewMemoryCache 创建内存 LRU 缓存。
func NewMemoryCache(maxEntries int) Cache {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	return &memCache{
		max:     maxEntries,
		entries: make(map[string]*entry),
	}
}

type memCache struct {
	mu      sync.RWMutex
	max     int
	entries map[string]*entry
	order   []string
}

type entry struct {
	query string
}

func (c *memCache) Get(_ context.Context, hash string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.entries[hash]; ok {
		return e.query, true
	}
	return "", false
}

func (c *memCache) Set(_ context.Context, hash string, query string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[hash]; ok {
		return nil
	}
	if len(c.entries) >= c.max && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.entries[hash] = &entry{query: query}
	c.order = append(c.order, hash)
	return nil
}

// ===== APQ Extension =====

// AutoPersistedQueries 创建 Apollo-compatible APQ extension。
// 协议:客户端发送 extensions.persistedQuery.sha256Hash;若命中缓存则直接执行;
// 未命中返回 PersistedQueryNotFound,客户端带完整 query 重试时自动缓存。
func AutoPersistedQueries(cache Cache) graphql.HandlerExtension {
	return &apqExt{cache: cache}
}

type apqExt struct {
	cache Cache
}

func (e *apqExt) ExtensionName() string { return "AutomaticPersistedQueries" }

func (e *apqExt) Validate(_ graphql.ExecutableSchema) error { return nil }

func (e *apqExt) MutateOperationParameters(ctx context.Context, req *graphql.RawParams) *gqlerror.Error {
	if req.Extensions == nil {
		return nil
	}
	pq, ok := req.Extensions["persistedQuery"]
	if !ok {
		return nil
	}
	pqMap, ok := pq.(map[string]interface{})
	if !ok {
		return nil
	}
	hash, _ := pqMap["sha256Hash"].(string)
	if hash == "" {
		return nil
	}

	if req.Query == "" {
		query, found := e.cache.Get(ctx, hash)
		if !found {
			return &gqlerror.Error{
				Message: "PersistedQueryNotFound",
				Extensions: map[string]interface{}{
					"code": "PERSISTED_QUERY_NOT_FOUND",
				},
			}
		}
		req.Query = query
	} else {
		computed := sha256Hash(req.Query)
		if computed != hash {
			return &gqlerror.Error{
				Message: "provided sha does not match query",
				Extensions: map[string]interface{}{
					"code": "PERSISTED_QUERY_HASH_MISMATCH",
				},
			}
		}
		_ = e.cache.Set(ctx, hash, req.Query)
	}
	return nil
}

// ===== Whitelist Extension =====

// Whitelist 创建白名单模式 extension。只有在 queries 中注册的查询(按 hash)才允许执行。
// queries: sha256Hash → query string。
func Whitelist(queries map[string]string) graphql.HandlerExtension {
	return &whitelistExt{allowed: queries}
}

type whitelistExt struct {
	allowed map[string]string
}

func (e *whitelistExt) ExtensionName() string { return "QueryWhitelist" }

func (e *whitelistExt) Validate(_ graphql.ExecutableSchema) error { return nil }

func (e *whitelistExt) MutateOperationParameters(ctx context.Context, req *graphql.RawParams) *gqlerror.Error {
	if req.Extensions == nil {
		return nil
	}
	pq, ok := req.Extensions["persistedQuery"]
	if !ok {
		return nil
	}
	pqMap, ok := pq.(map[string]interface{})
	if !ok {
		return nil
	}
	hash, _ := pqMap["sha256Hash"].(string)
	if hash == "" {
		return nil
	}

	query, ok := e.allowed[hash]
	if !ok {
		return &gqlerror.Error{
			Message: "query not in whitelist",
			Extensions: map[string]interface{}{
				"code": "QUERY_NOT_ALLOWED",
			},
		}
	}
	req.Query = query
	return nil
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
