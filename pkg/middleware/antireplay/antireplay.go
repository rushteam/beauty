// Package antireplay 提供基于 nonce 的 HTTP 防重放中间件。
//
// 每个请求携带一个唯一的 nonce（通过 header 传递），中间件用 kvstore.Store.SetNX
// 写入该 nonce；若 nonce 已存在则拒绝请求，从而阻止重放攻击。
//
// 典型用法：
//
//	store := redis.NewStore(redisClient)
//	mux.Use(antireplay.HTTPMiddleware(store,
//	    antireplay.WithSkipPrefixes("/healthz", "/callback/"),
//	))
package antireplay

import (
	"net/http"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/store/kvstore"
)

type options struct {
	header       string
	keyPrefix    string
	ttl          time.Duration
	skipPrefixes []string
	onReject     func(w http.ResponseWriter, reason string)
}

func defaults() *options {
	return &options{
		header:    "X-Nonce",
		keyPrefix: "nonce:",
		ttl:       10 * time.Minute,
		onReject:  defaultReject,
	}
}

// Option 配置 AntiReplay 中间件。
type Option func(*options)

// WithHeader 自定义 nonce 的 header 名称，默认 "X-Nonce"。
func WithHeader(name string) Option {
	return func(o *options) { o.header = name }
}

// WithKeyPrefix 自定义存储 key 的前缀，默认 "nonce:"。
func WithKeyPrefix(prefix string) Option {
	return func(o *options) { o.keyPrefix = prefix }
}

// WithTTL 自定义 nonce 的过期时间，默认 10 分钟。
func WithTTL(ttl time.Duration) Option {
	return func(o *options) { o.ttl = ttl }
}

// WithSkipPrefixes 指定跳过检查的 URL 路径前缀。
func WithSkipPrefixes(prefixes ...string) Option {
	return func(o *options) { o.skipPrefixes = append(o.skipPrefixes, prefixes...) }
}

// WithRejectHandler 自定义拒绝响应的写入方式。reason 为 "missing" 或 "replay"。
func WithRejectHandler(fn func(w http.ResponseWriter, reason string)) Option {
	return func(o *options) { o.onReject = fn }
}

// HTTPMiddleware 返回防重放 HTTP 中间件。
func HTTPMiddleware(store kvstore.Store, opts ...Option) func(http.Handler) http.Handler {
	o := defaults()
	for _, fn := range opts {
		fn(o)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkip(r.URL.Path, o.skipPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			nonce := r.Header.Get(o.header)
			if nonce == "" {
				o.onReject(w, "missing")
				return
			}

			ok, err := store.SetNX(r.Context(), o.keyPrefix+nonce, []byte("1"), o.ttl)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !ok {
				o.onReject(w, "replay")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func shouldSkip(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func defaultReject(w http.ResponseWriter, reason string) {
	switch reason {
	case "missing":
		http.Error(w, "missing nonce", http.StatusBadRequest)
	default:
		http.Error(w, "replay detected", http.StatusForbidden)
	}
}
