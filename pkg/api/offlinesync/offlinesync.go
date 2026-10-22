// Package offlinesync 定义 PWA / Background Sync 离线回放的服务端契约。
//
// 客户端（Service Worker）在网络恢复后把积压的写操作批量 POST 到本包 Handler；
// 每条操作携带稳定 id，服务端用 Idempotency-Key 语义保证至多一次副作用。
//
// 请求：
//
//	POST /sync
//	Content-Type: application/json
//	{"ops":[{"id":"uuid","method":"POST","path":"/api/orders","body":{...}}]}
//
// 响应：
//
//	{"results":[{"id":"uuid","status":201,"replayed":false,"body":{...}}]}
//
// 机制、不政策：本包只编排回放与幂等；鉴权、校验、业务逻辑由 Executor 注入。
package offlinesync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/middleware/idempotencykey"
	"github.com/rushteam/beauty/pkg/store/idempotency"
)

// Op 是一条待回放的客户端操作。
type Op struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// Request 批量回放请求体。
type Request struct {
	Ops []Op `json:"ops"`
}

// OpResult 单条操作结果。
type OpResult struct {
	ID       string          `json:"id"`
	Status   int             `json:"status"`
	Replayed bool            `json:"replayed"`
	Body     json.RawMessage `json:"body,omitempty"`
}

// Response 批量回放响应体。
type Response struct {
	Results []OpResult `json:"results"`
}

// Executor 执行单条回放操作（通常是业务 mux / handler）。
// 收到的 *http.Request 已设置 Method/URL/Body/Idempotency-Key。
type Executor interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

// Handler 处理批量离线回放。零值不可用，用 New 构造。
type Handler struct {
	exec  Executor
	store *idempotency.Store[cached]
	limit int
}

type cached struct {
	Status int
	Body   json.RawMessage
}

// Option 配置 Handler。
type Option func(*Handler)

// WithTTL 设置每条 op 幂等结果缓存时长（默认 24h，覆盖较长离线窗口）。
func WithTTL(d time.Duration) Option {
	return func(h *Handler) {
		h.store = idempotency.New[cached](idempotency.WithTTL(d))
	}
}

// WithMaxOps 限制单次批量条数（默认 50）。
func WithMaxOps(n int) Option {
	return func(h *Handler) { h.limit = n }
}

// New 创建离线回放 Handler。exec 通常是业务 http.ServeMux。
func New(exec Executor, opts ...Option) *Handler {
	h := &Handler{
		exec:  exec,
		store: idempotency.New[cached](idempotency.WithTTL(24 * time.Hour)),
		limit: 50,
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// ServeHTTP 实现 http.Handler。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if len(req.Ops) == 0 {
		http.Error(w, "empty ops", http.StatusBadRequest)
		return
	}
	if len(req.Ops) > h.limit {
		http.Error(w, "too many ops", http.StatusRequestEntityTooLarge)
		return
	}

	out := Response{Results: make([]OpResult, 0, len(req.Ops))}
	for _, op := range req.Ops {
		out.Results = append(out.Results, h.runOp(r.Context(), op))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) runOp(ctx context.Context, op Op) OpResult {
	if op.ID == "" || op.Path == "" || op.Method == "" {
		return OpResult{ID: op.ID, Status: http.StatusBadRequest, Body: rawMsg(`{"error":"invalid op"}`)}
	}
	method := strings.ToUpper(op.Method)
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return OpResult{ID: op.ID, Status: http.StatusBadRequest, Body: rawMsg(`{"error":"method not allowed for sync"}`)}
	}

	result, err, shared := h.store.Do(op.ID, func() (cached, error) {
		rec := httptest.NewRecorder()
		body := bytes.NewReader(op.Body)
		req := httptest.NewRequestWithContext(ctx, method, op.Path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(idempotencykey.Header, op.ID)
		h.exec.ServeHTTP(rec, req)
		c := cached{Status: rec.Code, Body: append(json.RawMessage(nil), rec.Body.Bytes()...)}
		if c.Status >= 500 {
			return c, errNotCached
		}
		return c, nil
	})
	_ = err
	return OpResult{
		ID:       op.ID,
		Status:   result.Status,
		Replayed: shared && err == nil,
		Body:     result.Body,
	}
}

var errNotCached = errSentinel("not cached")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

func rawMsg(s string) json.RawMessage { return json.RawMessage(s) }
