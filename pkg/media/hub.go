package media

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/rushteam/beauty/pkg/hls"
)

// Session 是一路直播流的运行时:一个 hls.Stream + 一个随 Release 取消的 Context。
// 外部(如 ffmpeg Supervisor)把自己绑定到 Context() 上,即可在该路流结束时自动收尾。
type Session struct {
	Key    string
	Stream *hls.Stream

	ctx    context.Context
	cancel context.CancelFunc
}

// Context 在该路流被 Release(结束)时取消。把 Supervisor 等后台任务绑到它上面即可随流停机。
func (s *Session) Context() context.Context { return s.ctx }

// Hub 管理多路直播流:streamKey → Session。解决"多路并发、重复推流、按 key 分发"。
// 并发安全。零值不可用,用 NewHub 构造。
type Hub struct {
	mu        sync.RWMutex
	sessions  map[string]*Session
	newStream func(key string) *hls.Stream
	metrics   *Metrics
	base      context.Context
}

// HubOption 配置 Hub。
type HubOption func(*Hub)

// WithStreamFactory 自定义每路流的 hls.Stream 构造(默认 hls.NewStream())。这是 policy:
// 窗口、分片时长、存储后端、LL-HLS 等都在这里定。
func WithStreamFactory(fn func(key string) *hls.Stream) HubOption {
	return func(h *Hub) {
		if fn != nil {
			h.newStream = fn
		}
	}
}

// WithMetrics 注入指标记录器(默认 NewMetrics(),基于 OTel 全局 Meter)。
func WithMetrics(m *Metrics) HubOption {
	return func(h *Hub) {
		if m != nil {
			h.metrics = m
		}
	}
}

// WithBaseContext 设置各 Session Context 的父 context(默认 context.Background())。
func WithBaseContext(ctx context.Context) HubOption {
	return func(h *Hub) {
		if ctx != nil {
			h.base = ctx
		}
	}
}

// NewHub 创建多路流管理器。
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		sessions:  make(map[string]*Session),
		newStream: func(string) *hls.Stream { return hls.NewStream() },
		base:      context.Background(),
	}
	for _, o := range opts {
		o(h)
	}
	if h.metrics == nil {
		h.metrics = NewMetrics()
	}
	return h
}

// Acquire 为 key 创建一路流并注册。若该 key 已在推流,返回 (nil, false) 表示拒绝(防抢流);
// 成功返回 (session, true)。通常在 rtmp 的 PublishFunc 里调:拿到 session 就用它的 Stream
// 组装 remux/转码;返回 false 时让 PublishFunc 返回 nil 拒绝这次推流。
func (h *Hub) Acquire(key string) (*Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.sessions[key]; exists {
		h.metrics.add(h.base, h.metrics.rejected, key, 1)
		return nil, false
	}
	ctx, cancel := context.WithCancel(h.base)
	sess := &Session{Key: key, Stream: h.newStream(key), ctx: ctx, cancel: cancel}
	h.sessions[key] = sess
	h.metrics.incActive(ctx, key, 1)
	h.metrics.add(ctx, h.metrics.publish, key, 1)
	return sess, true
}

// Release 结束一路流:取消 Session.Context(停掉绑定的后台任务)、把 Stream 封为点播、
// 从注册表移除。幂等。
func (h *Hub) Release(key string) {
	h.mu.Lock()
	sess := h.sessions[key]
	delete(h.sessions, key)
	h.mu.Unlock()
	if sess == nil {
		return
	}
	sess.cancel()
	sess.Stream.Finish()
	h.metrics.incActive(h.base, key, -1)
	h.metrics.add(h.base, h.metrics.unpublish, key, 1)
}

// Lookup 查找一路流。
func (h *Hub) Lookup(key string) (*Session, bool) {
	h.mu.RLock()
	sess, ok := h.sessions[key]
	h.mu.RUnlock()
	return sess, ok
}

// Count 返回当前在线流数。
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

// Metrics 返回指标记录器(供调用方上报 ingest bytes / segment 等)。
func (h *Hub) Metrics() *Metrics { return h.metrics }

// ServeHTTP 路由 /{key}/… 到对应流的 Stream(去掉 key 前缀);无此流返回 404。
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	sess, found := h.Lookup(key)
	if !found {
		http.NotFound(w, r)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + rest
	sess.Stream.ServeHTTP(w, r2)
}
