// Package idempotencykey 提供基于 HTTP Idempotency-Key 头的幂等中间件。
//
// 面向 PWA Background Sync / 离线回放：客户端为写操作带上稳定的幂等键，
// 网络重试或 SW 回放时服务端复用首次响应，避免重复副作用。
//
// 约定（机制）：
//   - 请求头 Idempotency-Key: <非空字符串>
//   - 仅对写方法（POST/PUT/PATCH/DELETE）生效；GET/HEAD 直接透传
//   - 缺省键时：RequireKey=false 则透传；RequireKey=true 则 400
//   - 同 key 复用首次成功（<500）响应；5xx 不缓存（允许随后重试）
//   - 复用响应时附加 X-Idempotency-Replayed: true
package idempotencykey

import (
	"bytes"
	"net/http"
	"time"

	"github.com/rushteam/beauty/pkg/store/idempotency"
)

// Header 是标准幂等键请求头名。
const Header = "Idempotency-Key"

// ReplayHeader 标记本次响应来自幂等缓存复用。
const ReplayHeader = "X-Idempotency-Replayed"

type cached struct {
	Status int
	Header http.Header
	Body   []byte
}

type config struct {
	ttl        time.Duration
	requireKey bool
	methods    map[string]bool
}

// Option 配置中间件。
type Option func(*config)

// WithTTL 设置响应缓存时长（默认 10 分钟）。
func WithTTL(d time.Duration) Option {
	return func(c *config) { c.ttl = d }
}

// WithRequireKey 为 true 时，写方法缺少 Idempotency-Key 返回 400。
func WithRequireKey(v bool) Option {
	return func(c *config) { c.requireKey = v }
}

// Middleware 返回幂等中间件。
func Middleware(opts ...Option) func(http.Handler) http.Handler {
	cfg := config{
		ttl: 10 * time.Minute,
		methods: map[string]bool{
			http.MethodPost: true, http.MethodPut: true,
			http.MethodPatch: true, http.MethodDelete: true,
		},
	}
	for _, o := range opts {
		o(&cfg)
	}
	store := idempotency.New[cached](idempotency.WithTTL(cfg.ttl))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.methods[r.Method] {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get(Header)
			if key == "" {
				if cfg.requireKey {
					http.Error(w, "missing Idempotency-Key", http.StatusBadRequest)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			result, err, shared := store.Do(key, func() (cached, error) {
				rec := &recorder{header: make(http.Header), status: http.StatusOK}
				next.ServeHTTP(rec, r)
				c := cached{
					Status: rec.status,
					Header: cloneHop(rec.header),
					Body:   append([]byte(nil), rec.buf.Bytes()...),
				}
				if c.Status >= 500 {
					return c, errNotCached
				}
				return c, nil
			})
			// 无论成功缓存还是 5xx 不缓存，result 都带有本次（或并发首个）响应快照。
			writeCached(w, result, shared && err == nil)
		})
	}
}

var errNotCached = errSentinel("not cached")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

func writeCached(w http.ResponseWriter, c cached, replayed bool) {
	for k, vs := range c.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if replayed {
		w.Header().Set(ReplayHeader, "true")
	}
	w.WriteHeader(c.Status)
	_, _ = w.Write(c.Body)
}

func cloneHop(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vs := range h {
		switch http.CanonicalHeaderKey(k) {
		case "Connection", "Keep-Alive", "Transfer-Encoding", "Trailer", "Upgrade":
			continue
		}
		out[k] = append([]string(nil), vs...)
	}
	return out
}

type recorder struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(code int)        { r.status = code }
func (r *recorder) Write(b []byte) (int, error) { return r.buf.Write(b) }
