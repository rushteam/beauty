package media

import (
	"context"
	"net/http"
	"strings"
	"sync"
)

// Stream 是 Hub 按 key 管理的一路流需要具备的能力:能分发播放请求(http.Handler),
// 且能在流结束时收尾(Finish)。pkg/hls.Stream(自研 origin)与 pkg/media/hlsmux.Bridge
// (gohlslib 后端)都满足它——Hub 因此与具体 HLS 实现解耦,不再绑死某一个。
type Stream interface {
	http.Handler
	// Finish 在该路流被 Release 时调用,做收尾(如封为点播 / 关闭 muxer)。应幂等。
	Finish()
}

// Session 是一路直播流的运行时:一个 Stream + 一个随 Release 取消的 Context。
// 外部(如 ffmpeg Supervisor)把自己绑定到 Context() 上,即可在该路流结束时自动收尾。
type Session[S Stream] struct {
	Key    string
	Stream S

	ctx    context.Context
	cancel context.CancelFunc
}

// Context 在该路流被 Release(结束)时取消。把 Supervisor 等后台任务绑到它上面即可随流停机。
func (s *Session[S]) Context() context.Context { return s.ctx }

// Hub 管理多路直播流:streamKey → Session。解决"多路并发、重复推流、按 key 分发"。
// 按流类型参数化(Hub[*hls.Stream] 或 Hub[*hlsmux.Bridge] 等)。并发安全。
// 零值不可用,用 NewHub 构造。
type Hub[S Stream] struct {
	mu        sync.RWMutex
	sessions  map[string]*Session[S]
	newStream func(key string) S
	metrics   *Metrics
	base      context.Context
}

// HubOption 配置 Hub(与流类型无关的项)。
type HubOption func(*hubConfig)

type hubConfig struct {
	metrics *Metrics
	base    context.Context
}

// WithMetrics 注入指标记录器(默认 NewMetrics(),基于 OTel 全局 Meter)。
func WithMetrics(m *Metrics) HubOption {
	return func(c *hubConfig) {
		if m != nil {
			c.metrics = m
		}
	}
}

// WithBaseContext 设置各 Session Context 的父 context(默认 context.Background())。
func WithBaseContext(ctx context.Context) HubOption {
	return func(c *hubConfig) {
		if ctx != nil {
			c.base = ctx
		}
	}
}

// NewHub 创建多路流管理器。newStream 是每路流的构造(policy:窗口、分片时长、存储/LL-HLS
// 等都在这里定);它同时决定了流类型 S。newStream 为 nil 会 panic。
func NewHub[S Stream](newStream func(key string) S, opts ...HubOption) *Hub[S] {
	if newStream == nil {
		panic("media: NewHub requires a non-nil stream factory")
	}
	cfg := hubConfig{base: context.Background()}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.metrics == nil {
		cfg.metrics = NewMetrics()
	}
	return &Hub[S]{
		sessions:  make(map[string]*Session[S]),
		newStream: newStream,
		metrics:   cfg.metrics,
		base:      cfg.base,
	}
}

// Acquire 为 key 创建一路流并注册。若该 key 已在推流,返回 (nil, false) 表示拒绝(防抢流);
// 成功返回 (session, true)。通常在 rtmp 的 PublishFunc 里调:拿到 session 就用它的 Stream
// 收流/分发;返回 false 时让 PublishFunc 返回 nil 拒绝这次推流。
func (h *Hub[S]) Acquire(key string) (*Session[S], bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.sessions[key]; exists {
		h.metrics.add(h.base, h.metrics.rejected, key, 1)
		return nil, false
	}
	ctx, cancel := context.WithCancel(h.base)
	sess := &Session[S]{Key: key, Stream: h.newStream(key), ctx: ctx, cancel: cancel}
	h.sessions[key] = sess
	h.metrics.incActive(ctx, key, 1)
	h.metrics.add(ctx, h.metrics.publish, key, 1)
	return sess, true
}

// Release 结束一路流:取消 Session.Context(停掉绑定的后台任务)、收尾 Stream(Finish)、
// 从注册表移除。幂等。
func (h *Hub[S]) Release(key string) {
	h.mu.Lock()
	sess, ok := h.sessions[key]
	delete(h.sessions, key)
	h.mu.Unlock()
	if !ok {
		return
	}
	sess.cancel()
	sess.Stream.Finish()
	h.metrics.incActive(h.base, key, -1)
	h.metrics.add(h.base, h.metrics.unpublish, key, 1)
}

// Lookup 查找一路流。
func (h *Hub[S]) Lookup(key string) (*Session[S], bool) {
	h.mu.RLock()
	sess, ok := h.sessions[key]
	h.mu.RUnlock()
	return sess, ok
}

// Count 返回当前在线流数。
func (h *Hub[S]) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

// Metrics 返回指标记录器(供调用方上报 ingest bytes / segment 等)。
func (h *Hub[S]) Metrics() *Metrics { return h.metrics }

// ServeHTTP 路由 /{key}/… 到对应流的 Stream(去掉 key 前缀);无此流返回 404。
func (h *Hub[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
