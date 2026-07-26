package wasm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// ==== "HTTP 中间件即 wasm 模块" ====
//
// ABI(guest 需实现):
//   - 导出 memory;
//   - alloc(i32 size) -> i32 ptr:分配 size 字节,返回起始地址(host 会把请求 JSON 写在这里);
//   - handle(i32 reqPtr, i32 reqLen) -> i64:处理后返回打包地址 (respPtr<<32 | respLen),
//     host 从该处读取"决策 JSON"(Decision)。
//
// host 每个请求:实例化 → 写入 Request(JSON)→ handle → 读回 Decision → 据其放行或拦截。
// 用 Go(//go:wasmexport)、TinyGo、Rust 等都可编写 guest(见 README 与 example)。

// Request 是传给 guest 的请求元数据(JSON)。v1 不含 body(避免大内存拷贝);需要可后续扩展。
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Body:仅当中间件启用 WithBody 时非空——请求体前 maxBytes 字节的 **base64**(二进制安全)。
	Body string `json:"body,omitempty"`
	// BodyTruncated:请求体超过 maxBytes、Body 被截断时为 true(下游仍收到完整 body)。
	BodyTruncated bool `json:"bodyTruncated,omitempty"`
}

// Decision 是 guest 返回的决策(JSON)。
type Decision struct {
	// Action:"next" 放行(交给下游);"deny" 拦截(用下面的字段写响应)。其它值按 deny 处理。
	Action string `json:"action"`
	// Status:deny 时的状态码(默认 403)。
	Status int `json:"status,omitempty"`
	// Headers:deny 时写入响应的头。
	Headers map[string]string `json:"headers,omitempty"`
	// Body:deny 时的响应体。
	Body string `json:"body,omitempty"`

	// —— 改写(仅 next 时生效):在放行给下游前修改请求。生效顺序 Set → Add → Remove ——
	// SetRequestHeaders:设置/覆盖请求头(替换该头的全部值,如认证后注入 X-User)。
	SetRequestHeaders map[string]string `json:"setRequestHeaders,omitempty"`
	// AddRequestHeaders:追加请求头值(保留已有值,可一键加多值,如多值的 X-Forwarded-For / trace 头)。
	AddRequestHeaders map[string][]string `json:"addRequestHeaders,omitempty"`
	// RemoveRequestHeaders:删除请求头(如剥离客户端伪造的内部头)。
	RemoveRequestHeaders []string `json:"removeRequestHeaders,omitempty"`
}

// mwConfig 配置中间件。
type mwConfig struct {
	failOpen bool
	allocFn  string
	handleFn string
	timeout  time.Duration
	poolSize int
	warm     int
	observer func(Event)
	bodyMax  int
}

// Event 是一次 wasm 中间件执行的可观测事件(执行后回调,用于接指标/日志/追踪)。
type Event struct {
	Action   string        // 最终动作:"next" / "deny" / "error"(出错或超时)
	Err      error         // 非 nil 表示执行出错(含超时)
	Duration time.Duration // 本次执行耗时(实例获取 + handle)
}

// MiddlewareOption 配置中间件。
type MiddlewareOption func(*mwConfig)

// WithPool 用大小为 size 的实例池复用 wasm 实例(而非每请求新建),降低重运行时 guest 的实例化开销。
// 注意:池化实例会被**复用**,guest 不能依赖"每次调用都是全新状态"——应在 handle 内自行重置
// (如复位其分配器)。出错/超时的实例不会放回(直接丢弃)。size<=0 表示不启用池(每请求新建)。
func WithPool(size int) MiddlewareOption { return func(c *mwConfig) { c.poolSize = size } }

// WithWarm 在中间件创建时预建 n 个空闲实例(需配合 WithPool)。n 超过池容量时按容量截断。
func WithWarm(n int) MiddlewareOption { return func(c *mwConfig) { c.warm = n } }

// WithObserver 注册执行后回调,收到 Event(动作/错误/耗时)——接 OTel/日志/指标由你定,故本包不绑具体实现。
func WithObserver(fn func(Event)) MiddlewareOption { return func(c *mwConfig) { c.observer = fn } }

// WithBody 让 guest 能看到请求体(默认**不**读 body,零成本)。maxBytes>0 时,中间件读取请求体前
// maxBytes 字节(base64 放进 Request.Body,超出置 BodyTruncated=true),并**完整还原**给下游——
// 因此 guest 可见部分受限(内存有界),下游仍收到完整 body。<=0 表示不启用。
func WithBody(maxBytes int) MiddlewareOption { return func(c *mwConfig) { c.bodyMax = maxBytes } }

// WithFailOpen 设置 wasm 出错时的行为:true=放行(可用性优先),false(默认)=拦截并返回 500
// (安全优先,适合鉴权/WAF 类过滤器)。执行超时也算"出错",按此策略处理。
func WithFailOpen(open bool) MiddlewareOption { return func(c *mwConfig) { c.failOpen = open } }

// WithTimeout 设置单次 wasm 执行的超时(实例化 + handle)。超时会**中断** guest 执行
// (依赖 Runtime 的 CloseOnContextDone,默认已开启),挡住死循环。<=0 表示不限时。
func WithTimeout(d time.Duration) MiddlewareOption { return func(c *mwConfig) { c.timeout = d } }

// WithFuncNames 覆盖 guest 的导出函数名(默认 "alloc" / "handle")。
func WithFuncNames(alloc, handle string) MiddlewareOption {
	return func(c *mwConfig) { c.allocFn, c.handleFn = alloc, handle }
}

// Middleware 返回一个 HTTP 中间件:每个请求实例化一次 mod、把请求元数据交给 guest 的 handle,
// 按返回的 Decision 放行或拦截。mod 由 Runtime.Compile 得到(编译一次,中间件内每请求实例化)。
//
// 注:每请求实例化保证隔离与并发安全;对带重运行时的 guest(如 Go 编译产物)开销较大,
// 后续可加实例池优化(v1 先求正确)。
func Middleware(mod *Module, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	cfg := mwConfig{allocFn: "alloc", handleFn: "handle"}
	for _, o := range opts {
		o(&cfg)
	}
	var pool *Pool
	if cfg.poolSize > 0 {
		pool = mod.NewPool(cfg.poolSize)
		if cfg.warm > 0 {
			_ = pool.Warm(context.Background(), cfg.warm)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			dec, err := runOnce(r, mod, pool, &cfg)
			if cfg.observer != nil {
				act := "error"
				if err == nil {
					if dec.Action == "next" {
						act = "next"
					} else {
						act = "deny"
					}
				}
				cfg.observer(Event{Action: act, Err: err, Duration: time.Since(start)})
			}
			if err != nil {
				if cfg.failOpen {
					next.ServeHTTP(w, r)
					return
				}
				http.Error(w, "wasm filter error", http.StatusInternalServerError)
				return
			}
			if dec.Action == "next" {
				// 改写:放行前按 guest 的决策修改请求头(Set 覆盖 → Add 追加 → Remove 删除)。
				for k, v := range dec.SetRequestHeaders {
					r.Header.Set(k, v)
				}
				for k, vs := range dec.AddRequestHeaders {
					for _, v := range vs {
						r.Header.Add(k, v)
					}
				}
				for _, k := range dec.RemoveRequestHeaders {
					r.Header.Del(k)
				}
				next.ServeHTTP(w, r)
				return
			}
			// deny
			for k, v := range dec.Headers {
				w.Header().Set(k, v)
			}
			status := dec.Status
			if status == 0 {
				status = http.StatusForbidden
			}
			w.WriteHeader(status)
			if dec.Body != "" {
				_, _ = w.Write([]byte(dec.Body))
			}
		})
	}
}

// runOnce 获取实例(池或新建)→ 交换 → 按结果归还:成功且用池则放回复用,否则关闭丢弃
// (超时被中断的实例已不可用,必须丢弃)。超时会中断执行。
func runOnce(r *http.Request, mod *Module, pool *Pool, cfg *mwConfig) (Decision, error) {
	ctx := r.Context()
	if cfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
	}
	var inst *Instance
	var err error
	if pool != nil {
		inst, err = pool.Get(ctx)
	} else {
		inst, err = mod.Instantiate(ctx)
	}
	if err != nil {
		return Decision{}, err
	}
	// 按需读取请求体(opt-in);读后已还原 r.Body,下游不受影响。
	var body []byte
	var truncated bool
	if cfg.bodyMax > 0 {
		if body, truncated, err = captureBody(r, cfg.bodyMax); err != nil {
			if pool != nil {
				pool.Put(context.Background(), inst)
			} else {
				_ = inst.Close(context.Background())
			}
			return Decision{}, err
		}
	}
	dec, err := exchange(ctx, inst, r, body, truncated, cfg)
	// 用 background 做归还/关闭,避免请求 ctx 已取消(超时)影响清理。
	if pool != nil && err == nil {
		pool.Put(context.Background(), inst)
	} else {
		_ = inst.Close(context.Background())
	}
	return dec, err
}

// exchange 在给定实例上跑一次:写请求→handle→读决策。
func exchange(ctx context.Context, inst *Instance, r *http.Request, body []byte, truncated bool, cfg *mwConfig) (Decision, error) {
	reqBytes, err := json.Marshal(buildRequest(r, body, truncated))
	if err != nil {
		return Decision{}, err
	}
	ptr, err := inst.WriteTo(ctx, cfg.allocFn, reqBytes)
	if err != nil {
		return Decision{}, err
	}
	res, err := inst.Call(ctx, cfg.handleFn, api.EncodeU32(ptr), api.EncodeU32(uint32(len(reqBytes))))
	if err != nil {
		return Decision{}, err
	}
	packed := res[0]
	respBytes, err := inst.ReadBytes(uint32(packed>>32), uint32(packed))
	if err != nil {
		return Decision{}, err
	}
	var dec Decision
	if err := json.Unmarshal(respBytes, &dec); err != nil {
		return Decision{}, err
	}
	return dec, nil
}

// _ 保持 api 依赖被 middleware.go 直接引用(EncodeU32),便于阅读。

func buildRequest(r *http.Request, body []byte, truncated bool) Request {
	h := make(map[string]string, len(r.Header))
	for k := range r.Header {
		h[k] = r.Header.Get(k)
	}
	req := Request{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.RawQuery,
		Headers: h,
	}
	if body != nil {
		req.Body = base64.StdEncoding.EncodeToString(body)
		req.BodyTruncated = truncated
	}
	return req
}

// captureBody 读取请求体前 max 字节供 guest 使用,并把**完整** body 还原回 r.Body 供下游读取
// (仅在内存中缓冲已读部分,超出部分仍以流的方式透传,内存有界)。
func captureBody(r *http.Request, max int) (head []byte, truncated bool, err error) {
	if r.Body == nil || max <= 0 {
		return nil, false, nil
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, int64(max)+1)) // 多读 1 字节探测是否超长
	if err != nil {
		return nil, false, err
	}
	if len(buf) > max {
		// 超长:guest 只见前 max 字节;下游读到 已读缓冲 + 剩余流 = 完整 body。
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		return buf[:max], true, nil
	}
	r.Body = io.NopCloser(bytes.NewReader(buf))
	return buf, false, nil
}
