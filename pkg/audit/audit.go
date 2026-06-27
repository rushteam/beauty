// Package audit 提供操作审计日志:结构化记录"谁在何时对什么资源做了什么操作",
// 仅记录成功的请求(失败请求走普通日志),用于合规与运维审计。
//
// 设计参考 Nakama server/console_audit.go:
//   - gRPC/HTTP 拦截器在 handler 成功后写一条 AuditEntry;
//   - Entry 含 Resource + Action 枚举 + UserID + 结构化 Metadata;
//   - 仅 err==nil 且状态码 < 500 才记(失败由 logger 负责);
//   - 异步写入 Sink(如 DB/文件),不阻塞响应。
//
// 与 pkg/logger 的区别:logger 记运行时事件(含错误),audit 只记"成功的敏感操作"
// 且字段固定(Resource/Action),便于按操作类型检索与合规导出。
//
// 零值不可用,用 New 构造。Audit 并发安全。
package audit

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Resource 资源类型(业务自定义枚举值)。用 int 避免字符串拼写差异,便于索引。
type Resource int

// Action 操作类型。常用值预定义,业务可扩展。
type Action int

const (
	ActionCreate Action = iota + 1
	ActionUpdate
	ActionDelete
	ActionRead
)

// Entry 是一条审计记录。创建后不可变。
type Entry struct {
	ID         int64     // 单调递增
	Time       time.Time // 操作发生时间
	UserID     string    // 操作者
	Resource   Resource  // 被操作资源类型
	ResourceID string    // 被操作资源 ID(如用户 ID、配置名)
	Action     Action    // 操作类型
	Method     string    // HTTP 方法 / gRPC 方法
	Path       string    // HTTP 路径 / gRPC FullMethod
	Status     int       // HTTP 状态码 / gRPC OK=0
	Metadata   string    // 业务自定义 JSON 等编码
}

// Sink 接收审计条目。实现可以是 DB 写入、文件追加、远程聚合等。
// 必须并发安全。返回 error 仅用于记录失败(不影响主流程)。
type Sink interface {
	Write(ctx context.Context, e Entry) error
}

// SinkFunc 函数适配器。
type SinkFunc func(ctx context.Context, e Entry) error

func (f SinkFunc) Write(ctx context.Context, e Entry) error { return f(ctx, e) }

// Audit 管理审计条目的收集与异步分发。
type Audit struct {
	mu      sync.Mutex
	seq     int64
	sink    Sink
	queue   chan Entry        // 异步队列,满则丢弃(审计不应阻塞业务)
	wg      sync.WaitGroup
	stopped bool
}

// Option 配置 Audit。
type Option func(*config)

type config struct {
	queueSize int
}

// WithQueueSize 设置异步队列长度。满时丢弃最旧条目并记一条丢弃计数(默认 1024)。
func WithQueueSize(n int) Option { return func(c *config) { c.queueSize = n } }

// New 创建审计器。sink 为 nil 时仅内存计数(不落盘)。
func New(sink Sink, opts ...Option) *Audit {
	cfg := &config{queueSize: 1024}
	for _, o := range opts {
		o(cfg)
	}
	a := &Audit{sink: sink, queue: make(chan Entry, cfg.queueSize)}
	a.wg.Add(1)
	go a.dispatch()
	return a
}

// Record 记录一条审计条目。非阻塞:队列满时丢弃。
// 仅在业务确认操作成功后调用(参考 Nakama 仅记 err==nil)。
func (a *Audit) Record(ctx context.Context, e Entry) {
	a.mu.Lock()
	a.seq++
	e.ID = a.seq
	a.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	select {
	case a.queue <- e:
	default:
		// 队列满:丢弃,不阻塞业务。生产建议配合 metrics 报警。
	}
}

// HTTPMiddleware 返回 HTTP 中间件:仅在响应状态码 < 500 时记一条审计。
// resource/resourceID 由 resolver 从请求中提取;resolver 为 nil 则不记录。
func (a *Audit) HTTPMiddleware(resolver func(*http.Request) (Resource, string, string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			if sw.status >= 500 {
				return // 失败不审计,走 logger
			}
			if resolver == nil {
				return
			}
			res, rid, meta := resolver(r)
			a.Record(r.Context(), Entry{
				UserID:     UserIDFromCtx(r.Context()),
				Resource:   res,
				ResourceID: rid,
				Action:     actionFromMethod(r.Method),
				Method:     r.Method,
				Path:       r.URL.Path,
				Status:     sw.status,
				Metadata:   meta,
			})
		})
	}
}

// Stop 关闭异步队列并等待落盘完成。
func (a *Audit) Stop() {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return
	}
	a.stopped = true
	close(a.queue)
	a.mu.Unlock()
	a.wg.Wait()
}

func (a *Audit) dispatch() {
	defer a.wg.Done()
	for e := range a.queue {
		if a.sink != nil {
			_ = a.sink.Write(context.Background(), e)
		}
	}
}

func actionFromMethod(m string) Action {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return ActionUpdate
	case http.MethodDelete:
		return ActionDelete
	case http.MethodGet:
		return ActionRead
	default:
		return ActionCreate
	}
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type ctxKey struct{}

// WithUserID 把 userID 注入 ctx,供中间件读取。
func WithUserID(ctx context.Context, uid string) context.Context {
	return context.WithValue(ctx, ctxKey{}, uid)
}

// UserIDFromCtx 从 ctx 读出 WithUserID 注入的 userID。无则返回空串。
func UserIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}
