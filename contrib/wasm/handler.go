package wasm

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// ==== "wasm 函数即 HTTP Handler"——FaaS-lite ====
//
// 与 Middleware 的区别:Middleware 是中间件(Decision: next/deny),Handler 是终端处理器
// (直接产生完整 HTTP 响应)。guest ABI 相同(alloc/handle),但输出是 Response 而非 Decision。
//
// 用途:把用户上传的 .wasm 模块直接暴露为 HTTP 端点——纯计算、无状态的 serverless 函数。

// Response 是 guest 返回的 HTTP 响应(JSON)。
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// HandlerConfig 配置 FaaS handler。
type HandlerConfig struct {
	allocFn  string
	handleFn string
	timeout  time.Duration
	poolSize int
	bodyMax  int
}

// HandlerOption 配置 wasm handler。
type HandlerOption func(*HandlerConfig)

// WithHandlerTimeout 设置单次执行超时。
func WithHandlerTimeout(d time.Duration) HandlerOption {
	return func(c *HandlerConfig) { c.timeout = d }
}

// WithHandlerPool 设置实例池大小。
func WithHandlerPool(size int) HandlerOption {
	return func(c *HandlerConfig) { c.poolSize = size }
}

// WithHandlerBody 设置请求体最大可见字节(base64 传给 guest)。
func WithHandlerBody(maxBytes int) HandlerOption {
	return func(c *HandlerConfig) { c.bodyMax = maxBytes }
}

// WithHandlerFuncNames 覆盖 guest 导出函数名。
func WithHandlerFuncNames(alloc, handle string) HandlerOption {
	return func(c *HandlerConfig) { c.allocFn, c.handleFn = alloc, handle }
}

// Handler 把一个 wasm 模块包装成 http.Handler:每个请求传入 Request JSON,guest 返回
// Response JSON,host 据此写 HTTP 响应。与 Middleware 共享同一 guest ABI(alloc/handle),
// 仅输出格式不同(Response vs Decision)。
func Handler(mod *Module, opts ...HandlerOption) http.Handler {
	cfg := &HandlerConfig{allocFn: "alloc", handleFn: "handle", timeout: 30 * time.Second}
	for _, o := range opts {
		o(cfg)
	}
	var pool *Pool
	if cfg.poolSize > 0 {
		pool = mod.NewPool(cfg.poolSize)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			http.Error(w, "wasm: instance error", http.StatusInternalServerError)
			return
		}

		var body []byte
		var truncated bool
		if cfg.bodyMax > 0 {
			if body, truncated, err = captureBody(r, cfg.bodyMax); err != nil {
				closeOrPut(pool, inst, true)
				http.Error(w, "wasm: read body error", http.StatusInternalServerError)
				return
			}
		}

		resp, err := callHandler(ctx, inst, r, body, truncated, cfg)
		if err != nil {
			closeOrPut(pool, inst, true)
			http.Error(w, "wasm: execution error", http.StatusInternalServerError)
			return
		}
		closeOrPut(pool, inst, false)

		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
		status := resp.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if resp.Body != "" {
			_, _ = w.Write([]byte(resp.Body))
		}
	})
}

func callHandler(ctx context.Context, inst *Instance, r *http.Request, body []byte, truncated bool, cfg *HandlerConfig) (Response, error) {
	reqBytes, err := json.Marshal(buildRequest(r, body, truncated))
	if err != nil {
		return Response{}, err
	}
	ptr, err := inst.WriteTo(ctx, cfg.allocFn, reqBytes)
	if err != nil {
		return Response{}, err
	}
	res, err := inst.Call(ctx, cfg.handleFn, encodeU32(ptr), encodeU32(uint32(len(reqBytes))))
	if err != nil {
		return Response{}, err
	}
	packed := res[0]
	respBytes, err := inst.ReadBytes(uint32(packed>>32), uint32(packed))
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func closeOrPut(pool *Pool, inst *Instance, failed bool) {
	if pool != nil && !failed {
		pool.Put(context.Background(), inst)
	} else {
		_ = inst.Close(context.Background())
	}
}

func encodeU32(v uint32) uint64 { return uint64(v) }
