// Package handler 提供声明式 HTTP handler 包装器:把 auth 策略 + 资源注入 +
// 错误归一化从业务 handler 上移到包装器,handler 只关心 (ctx, req) => (resp, error)。
//
// 声明式声明"这个 handler 需要什么"(认证策略、依赖),由包装器统一注入,
// 业务函数保持纯净——只接收 ctx 和解析好的请求,返回响应或 error。
//
// 这其实是 pkg/callbacks + pkg/middleware/auth + pkg/afterwork + DI
// 组合成一个 ergonomic 的 handler 装饰器:
//   - WithAuth(policy):在调业务函数前做认证/授权,User 注入 ctx;
//   - WithInject(deps):把任意依赖(DB、cache、client...)装入 ctx 供 handler 取;
//   - WithAfterwork:挂上 afterwork.Middleware,handler 里 afterwork.Defer(...)
//     投递的响应后副作用在响应返回后跑完;
//   - WithRatelimit:声明式限流;
//   - WithMiddleware:挂任意标准中间件(func(http.Handler) http.Handler)于最外层——
//     核心不依赖 contrib,故可即插即用如 contrib/wasm 的过滤器等;
//   - 返回的 error 自动经 errors.WriteHTTP 归一化为统一错误响应。
//
// 用法:
//
//	type CreateOrderReq struct { UserID string; Sku string }
//	type CreateOrderResp struct { OrderID string }
//
//	h := handler.New[CreateOrderReq, CreateOrderResp](
//	    "POST /orders",
//	    func(ctx context.Context, req *CreateOrderReq) (*CreateOrderResp, error) {
//	        user := auth.GetUserFromContext(ctx)      // 认证后注入
//	        db := handler.MustGet[*sql.DB](ctx, "db") // 依赖注入
//	        id, err := createOrder(ctx, db, user.ID(), req.Sku)
//	        if err != nil { return nil, err }
//	        afterwork.Defer(ctx, func(c context.Context) { // 响应后副作用
//	            _ = webhook.Notify(c, orderEvent{id})
//	        })
//	        return &CreateOrderResp{OrderID: id}, nil
//	    },
//	    handler.WithAuth(authPolicy),
//	    handler.WithInject("db", orderDB),
//	    handler.WithAfterwork(),
//	)
//	mux.Handle("/orders", h)
//
// 泛型参数 I 是请求体类型(指针),O 是响应体类型(指针),均通过 JSON 编解码。
// 请求方法固定为方法字段(Method);query/path 参数由调用方自行从 *http.Request
// 取(本包装器只管 body + auth + error + 响应后副作用)。
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/rushteam/beauty/pkg/afterwork"
	perr "github.com/rushteam/beauty/pkg/errors"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/ratelimit"
)

// depKey 用作 ctx value 的依赖键(字符串)。
type depKey string

// Func 业务 handler 函数签名:接收 ctx 和解析好的请求体,返回响应体或 error。
// ctx 已注入认证后的 User(若有 WithAuth)和声明式依赖(若有 WithInject)。
type Func[I any, O any] func(ctx context.Context, req *I) (*O, error)

// AuthPolicy 声明式认证策略:由调用方实现,返回认证后的 User 或 error。
// 通常包装 pkg/middleware/auth.Authenticator.Authenticate + Authorizer.Authorize。
// 返回的 User 会被注入 ctx(供业务函数用 auth.GetUserFromContext 取出)。
// resource/action 用于授权检查;返回 nil User 表示匿名访问(允许则放行)。
type AuthPolicy func(ctx context.Context, r *http.Request) (auth.User, error)

// Handler 包装后的 http.Handler。实现 http.Handler 接口,可直接挂到 mux。
type Handler[I any, O any] struct {
	method    string
	fn        Func[I, O]
	auth      AuthPolicy
	deps      map[string]any
	afterMW   bool
	afterOpts []afterwork.Option
	rlLimiter ratelimit.Limiter
	rlKeyFn   ratelimit.KeyFunc
	mws       []func(http.Handler) http.Handler
}

// Option 配置 Handler。
type Option func(*config)

type config struct {
	method    string
	auth      AuthPolicy
	deps      map[string]any
	after     bool
	afterOpts []afterwork.Option
	rlLimiter ratelimit.Limiter
	rlKeyFn   ratelimit.KeyFunc
	mws       []func(http.Handler) http.Handler
}

// WithMethod 设置允许的 HTTP 方法(如 "POST")。空表示不限。
func WithMethod(m string) Option { return func(c *config) { c.method = m } }

// WithAuth 附加声明式认证策略。handler 执行前先调 policy:
// 失败则经 errors.WriteHTTP 返回错误响应;成功则把 User 注入 ctx。
func WithAuth(p AuthPolicy) Option { return func(c *config) { c.auth = p } }

// WithInject 注入一个命名依赖到 ctx,业务函数用 Get[T](ctx, name) 取出。
// 可多次调用注入多个依赖。
func WithInject(name string, dep any) Option {
	return func(c *config) {
		if c.deps == nil {
			c.deps = make(map[string]any)
		}
		c.deps[name] = dep
	}
}

// WithAfterwork 挂载 afterwork.Middleware:handler 里 afterwork.Defer(...)
// 投递的响应后副作用,在响应返回后由中间件等待跑完。opts 透传给 afterwork。
func WithAfterwork(opts ...afterwork.Option) Option {
	return func(c *config) { c.after = true; c.afterOpts = opts }
}

// WithRatelimit 附加声明式限流:limiter 按 keyFn 提取的 key 限流,
// 超限返回 429 + Retry-After。限流在认证前执行(超限连 body 都不解析)。
// 传 nil limiter 表示不限流。
func WithRatelimit(l ratelimit.Limiter, keyFn ratelimit.KeyFunc) Option {
	return func(c *config) { c.rlLimiter = l; c.rlKeyFn = keyFn }
}

// WithMiddleware 附加任意标准 HTTP 中间件(func(http.Handler) http.Handler),挂在包装链的
// **最外层**——先于 ratelimit/afterwork/auth 执行,可提前短路(拒绝/改写)。多次传入或一次传多个时,
// **靠前的在更外层**(WithMiddleware(a, b) 中 a 包住 b 包住其余)。
//
// 核心不依赖 contrib,故通过这个通用口即插即用任意中间件——例如把 contrib/wasm 的过滤器绑上:
//
//	handler.New(method, fn,
//	    handler.WithMiddleware(wasm.Middleware(mod)), // wasm 沙箱过滤器
//	    handler.WithRatelimit(lim, keyFn),
//	)
func WithMiddleware(mw ...func(http.Handler) http.Handler) Option {
	return func(c *config) { c.mws = append(c.mws, mw...) }
}

// New 创建声明式 Handler。method 可为空(不限方法);fn 是业务函数。
// opts 依次应用 WithAuth / WithInject / WithAfterwork 等。
func New[I any, O any](method string, fn Func[I, O], opts ...Option) *Handler[I, O] {
	cfg := config{method: method}
	for _, o := range opts {
		o(&cfg)
	}
	return &Handler[I, O]{
		method:    cfg.method,
		fn:        fn,
		auth:      cfg.auth,
		deps:      cfg.deps,
		afterMW:   cfg.after,
		afterOpts: cfg.afterOpts,
		rlLimiter: cfg.rlLimiter,
		rlKeyFn:   cfg.rlKeyFn,
		mws:       cfg.mws,
	}
}

// ServeHTTP 实现 http.Handler。
// 包装顺序(由外到内):WithMiddleware(用户中间件)→ ratelimit → afterwork → handle(auth+inject+body+fn)。
// 用户中间件最外层(可提前短路);限流次之(超限不解析 body);afterwork 再次(响应后副作用跑完才放行)。
func (h *Handler[I, O]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var handler http.Handler = http.HandlerFunc(h.handle)
	if h.afterMW {
		handler = afterwork.Middleware(h.afterOpts...)(handler)
	}
	if h.rlLimiter != nil && h.rlKeyFn != nil {
		handler = ratelimit.Middleware(h.rlLimiter, h.rlKeyFn)(handler)
	}
	// 用户中间件挂最外层;倒序应用使靠前者在更外层。
	for i := len(h.mws) - 1; i >= 0; i-- {
		handler = h.mws[i](handler)
	}
	handler.ServeHTTP(w, r)
}

// handle 是纯处理函数:方法校验 → 依赖注入 → 认证 → 解析 body → 调业务函数 → 归一化错误。
func (h *Handler[I, O]) handle(w http.ResponseWriter, r *http.Request) {
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
	resp, err := h.fn(ctx, &req)
	if err != nil {
		writeErr(w, err)
		return
	}
	if resp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// Get 从 ctx 取出命名依赖并断言为 *T。不存在或类型不符返回 nil,false。
// 业务函数中典型用法:db := handler.MustGet[*sql.DB](ctx, "db")。
func Get[T any](ctx context.Context, name string) (T, bool) {
	v := ctx.Value(depKey(name))
	t, ok := v.(T)
	return t, ok
}

// MustGet 同 Get,但类型不符/不存在时 panic(用于启动期配置错误,早炸)。
func MustGet[T any](ctx context.Context, name string) T {
	v, ok := Get[T](ctx, name)
	if !ok {
		var zero T
		panic("handler: dependency " + name + " not found or wrong type, want " + reflect.TypeOf(zero).String())
	}
	return v
}

// writeErr 把任意 error 归一化为 *Status 并写入 HTTP 响应。
// 已是 *Status 则原样用;普通 error 兜底 CodeInternal(500)。
func writeErr(w http.ResponseWriter, err error) {
	st, ok := perr.FromError(err)
	if !ok {
		st = perr.New(perr.CodeInternal, err.Error())
	}
	perr.WriteHTTP(w, st)
}

// hasBody 判断请求是否带 body(POST/PUT/PATCH 通常带)。
func hasBody(r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	// ContentLength==-1 表示 chunked(未知长度),视为有 body。
	return r.ContentLength != 0
}
