package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	perr "github.com/rushteam/beauty/pkg/api/errors"
	"github.com/rushteam/beauty/pkg/middleware/auth"
)

// AsyncFunc 异步 handler 函数签名:接收 ctx、请求体和 respond 回调。
// 业务调用 respond 返回响应(可在另一 goroutine 中调用),适合长耗时异步操作。
//
// respond 只能调用一次——第二次调用返回 false。
// 如果在 http.ResponseWriter flush 前还没调 respond,框架会阻塞等待(超时由
// WithAsyncTimeout 控制)。
//
// 典型场景:LLM 推理、外部 RPC 聚合、异步审核等需要较长时间的请求。
//
//	handler.NewAsync[ChatReq, ChatResp]("POST", func(ctx context.Context, req *ChatReq, respond handler.Respond[ChatResp]) {
//	    go func() {
//	        result, err := callLLM(ctx, req.Prompt)
//	        if err != nil {
//	            respond(nil, err)
//	            return
//	        }
//	        respond(&ChatResp{Answer: result}, nil)
//	    }()
//	})
type AsyncFunc[I any, O any] func(ctx context.Context, req *I, respond Respond[O])

// Respond 回调:业务调用它返回响应或错误。只能调用一次。
type Respond[O any] func(resp *O, err error) bool

// AsyncHandler 包装异步 handler。实现 http.Handler 接口。
type AsyncHandler[I any, O any] struct {
	method string
	fn     AsyncFunc[I, O]
	auth   AuthPolicy
	deps   map[string]any
	mws    []func(http.Handler) http.Handler
}

// NewAsync 创建异步 Handler。fn 接收 respond 回调,可在任意 goroutine 中调用。
// opts 同 New:WithAuth / WithInject / WithMiddleware 等。
//
// 注意:WithAfterwork 不可用于异步 handler(响应时机由业务控制)。
func NewAsync[I any, O any](method string, fn AsyncFunc[I, O], opts ...Option) *AsyncHandler[I, O] {
	cfg := config{method: method}
	for _, o := range opts {
		o(&cfg)
	}
	return &AsyncHandler[I, O]{
		method: cfg.method,
		fn:     fn,
		auth:   cfg.auth,
		deps:   cfg.deps,
		mws:    cfg.mws,
	}
}

// ServeHTTP 实现 http.Handler。
func (h *AsyncHandler[I, O]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var handler http.Handler = http.HandlerFunc(h.handle)
	for i := len(h.mws) - 1; i >= 0; i-- {
		handler = h.mws[i](handler)
	}
	handler.ServeHTTP(w, r)
}

func (h *AsyncHandler[I, O]) handle(w http.ResponseWriter, r *http.Request) {
	if h.method != "" && r.Method != h.method {
		writeErr(w, perr.New(perr.CodeUnimplemented, "method not allowed: "+r.Method))
		return
	}
	ctx := r.Context()
	for name, dep := range h.deps {
		ctx = context.WithValue(ctx, depKey(name), dep)
	}
	if h.auth != nil {
		user, err := h.auth(ctx, r)
		if err != nil {
			writeErr(w, err)
			return
		}
		if user != nil {
			ctx = auth.WithUser(ctx, user)
		}
	}
	var req I
	if hasBody(r) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, perr.New(perr.CodeInvalidArgument, "invalid request body: "+err.Error()))
			return
		}
	}

	// 创建 respond 通道:业务回调通过它把结果送回 HTTP 处理 goroutine。
	type result struct {
		resp *O
		err  error
	}
	ch := make(chan result, 1)
	responded := make(chan struct{})
	var once sync.Once

	respond := func(resp *O, err error) bool {
		ok := false
		once.Do(func() {
			ch <- result{resp: resp, err: err}
			close(responded)
			ok = true
		})
		return ok
	}

	h.fn(ctx, &req, respond)

	// 等待业务回调或请求 ctx 取消。
	select {
	case res := <-ch:
		if res.err != nil {
			writeErr(w, res.err)
			return
		}
		if res.resp == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(res.resp)
	case <-ctx.Done():
		writeErr(w, perr.New(perr.CodeDeadline, "request timeout"))
	}
}
