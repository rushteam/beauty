package proxywasm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// FilterEvent 是一次 Proxy-Wasm 过滤器执行的可观测事件。
type FilterEvent struct {
	Action   string        // "continue" / "local_response" / "error"
	Status   int           // 最终 HTTP 状态码
	Err      error         // 非 nil 表示执行出错
	Duration time.Duration // 本次执行耗时
}

// FilterOption 配置 HTTPFilter。
type FilterOption func(*filterConfig)

type filterConfig struct {
	vmConfig     []byte
	pluginConfig []byte
	poolSize     int
	warm         int
	timeout      time.Duration
	failOpen     bool
	logLevel     LogLevel
	properties   map[string][]byte
	observer     func(FilterEvent)
}

// WithVMConfig 设置 VM configuration(proxy_on_vm_start 时通过 BufferTypeVMConfiguration 读取)。
func WithVMConfig(data []byte) FilterOption {
	return func(c *filterConfig) { c.vmConfig = data }
}

// WithPluginConfig 设置 Plugin configuration(proxy_on_configure 时通过 BufferTypePluginConfiguration 读取)。
func WithPluginConfig(data []byte) FilterOption {
	return func(c *filterConfig) { c.pluginConfig = data }
}

// WithPoolSize 设置实例池大小。<=0 则每请求新建(性能差但隔离好)。
func WithPoolSize(n int) FilterOption {
	return func(c *filterConfig) { c.poolSize = n }
}

// WithWarm 启动时预建实例数(需配合 WithPoolSize)。
func WithWarm(n int) FilterOption {
	return func(c *filterConfig) { c.warm = n }
}

// WithTimeout 设置单次请求的 wasm 执行超时。
func WithTimeout(d time.Duration) FilterOption {
	return func(c *filterConfig) { c.timeout = d }
}

// WithFailOpen 出错时放行(true)还是返回 500(false,默认)。
func WithFailOpen(open bool) FilterOption {
	return func(c *filterConfig) { c.failOpen = open }
}

// WithFilterLogLevel 设置此 filter 的日志级别。
func WithFilterLogLevel(l LogLevel) FilterOption {
	return func(c *filterConfig) { c.logLevel = l }
}

// WithProperties 注入自定义 properties。
func WithProperties(m map[string][]byte) FilterOption {
	return func(c *filterConfig) { c.properties = m }
}

// WithObserver 注册可观测回调。
func WithObserver(fn func(FilterEvent)) FilterOption {
	return func(c *filterConfig) { c.observer = fn }
}

// HTTPFilter 返回一个标准 HTTP 中间件,内部运行 Proxy-Wasm 插件的完整 HTTP stream 生命周期。
// 与 beauty 集成: webserver.WithMiddleware(proxywasm.HTTPFilter(mod, opts...))。
func HTTPFilter(mod *Module, opts ...FilterOption) func(http.Handler) http.Handler {
	cfg := filterConfig{logLevel: LogInfo}
	for _, o := range opts {
		o(&cfg)
	}

	var pool *Pool
	if cfg.poolSize > 0 {
		pool = newPool(mod, cfg.poolSize, cfg.vmConfig, cfg.pluginConfig, cfg.logLevel, cfg.properties)
		if cfg.warm > 0 {
			_ = pool.Warm(context.Background(), cfg.warm)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			ctx := r.Context()
			if cfg.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, cfg.timeout)
				defer cancel()
			}

			// 获取实例
			inst, err := getInstance(ctx, mod, pool, &cfg)
			if err != nil {
				handleError(w, r, next, err, &cfg, start)
				return
			}

			// 读取请求 body
			var reqBody []byte
			if r.Body != nil {
				reqBody, err = io.ReadAll(r.Body)
				if err != nil {
					putOrClose(ctx, pool, inst, true)
					handleError(w, r, next, err, &cfg, start)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(reqBody))
			}

			// 请求阶段
			lr, modHeaders, modBody, streamID, err := inst.handleHTTP(ctx, r, reqBody)
			if err != nil {
				putOrClose(ctx, pool, inst, true)
				handleError(w, r, next, err, &cfg, start)
				return
			}

			// 短路: send_local_response
			if lr != nil {
				inst.finishStream(context.Background(), streamID)
				putOrClose(context.Background(), pool, inst, false)
				writeLocalResponse(w, lr)
				observe(&cfg, "local_response", lr.status, nil, start)
				return
			}

			// 应用修改后的请求头到原始请求
			if modHeaders != nil {
				applyModifiedHeaders(r, modHeaders)
			}
			if modBody != nil {
				r.Body = io.NopCloser(bytes.NewReader(modBody))
				r.ContentLength = int64(len(modBody))
			}

			// 调用下游
			cw := &captureWriter{ResponseWriter: w, statusCode: 200}
			next.ServeHTTP(cw, r)

			// 响应阶段
			lr, respHeaders, respBody, err := inst.handleResponse(ctx, streamID, cw.statusCode, cw.Header().Clone(), cw.body.Bytes())
			if err != nil {
				// 响应阶段出错,但下游已执行,直接写已有响应
				inst.finishStream(context.Background(), streamID)
				putOrClose(context.Background(), pool, inst, true)
				flushCapturedResponse(w, cw)
				observe(&cfg, "error", cw.statusCode, err, start)
				return
			}

			inst.finishStream(context.Background(), streamID)
			putOrClose(context.Background(), pool, inst, false)

			if lr != nil {
				writeLocalResponse(w, lr)
				observe(&cfg, "local_response", lr.status, nil, start)
				return
			}

			// 写最终响应
			if respHeaders != nil {
				for k, vs := range respHeaders {
					w.Header()[k] = vs
				}
			}
			w.WriteHeader(cw.statusCode)
			if respBody != nil {
				w.Write(respBody)
			}
			observe(&cfg, "continue", cw.statusCode, nil, start)
		})
	}
}

func getInstance(ctx context.Context, mod *Module, pool *Pool, cfg *filterConfig) (*instance, error) {
	if pool != nil {
		return pool.Get(ctx)
	}
	raw, err := mod.instantiate(ctx)
	if err != nil {
		return nil, err
	}
	rt := mod.rt
	return initInstance(ctx, raw.mod, cfg.vmConfig, cfg.pluginConfig, cfg.logLevel, cfg.properties, rt.sharedData, rt.metrics, rt.sharedQueue, rt.foreignFuncs, rt.dispatcher)
}

func putOrClose(ctx context.Context, pool *Pool, inst *instance, failed bool) {
	if failed || pool == nil {
		_ = inst.close(ctx)
		return
	}
	pool.Put(ctx, inst)
}

func handleError(w http.ResponseWriter, r *http.Request, next http.Handler, err error, cfg *filterConfig, start time.Time) {
	observe(cfg, "error", 500, err, start)
	if cfg.failOpen {
		next.ServeHTTP(w, r)
		return
	}
	http.Error(w, "proxy-wasm filter error", http.StatusInternalServerError)
}

func writeLocalResponse(w http.ResponseWriter, lr *localResp) {
	if lr.headers != nil {
		for k, vs := range lr.headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
	}
	status := lr.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if lr.body != nil {
		w.Write(lr.body)
	}
}

func observe(cfg *filterConfig, action string, status int, err error, start time.Time) {
	if cfg.observer != nil {
		cfg.observer(FilterEvent{
			Action:   action,
			Status:   status,
			Err:      err,
			Duration: time.Since(start),
		})
	}
}

// applyModifiedHeaders 把 Proxy-Wasm 修改后的 headers 应用回请求(去除伪头)。
func applyModifiedHeaders(r *http.Request, h http.Header) {
	// 保留伪头信息更新请求字段
	if method := h.Get(":method"); method != "" {
		r.Method = method
	}
	if path := h.Get(":path"); path != "" {
		r.RequestURI = path
		if u, err := http.NewRequest(r.Method, path, nil); err == nil {
			r.URL = u.URL
		}
	}
	if authority := h.Get(":authority"); authority != "" {
		r.Host = authority
	}

	// 清除伪头
	h.Del(":method")
	h.Del(":path")
	h.Del(":authority")
	h.Del(":scheme")

	// 替换所有请求头
	r.Header = h
}

// captureWriter 捕获下游 handler 写入的响应。
type captureWriter struct {
	http.ResponseWriter
	statusCode  int
	body        bytes.Buffer
	headersSent bool
}

func (cw *captureWriter) WriteHeader(code int) {
	if !cw.headersSent {
		cw.statusCode = code
		cw.headersSent = true
	}
}

func (cw *captureWriter) Write(b []byte) (int, error) {
	if !cw.headersSent {
		cw.statusCode = 200
		cw.headersSent = true
	}
	return cw.body.Write(b)
}

func flushCapturedResponse(w http.ResponseWriter, cw *captureWriter) {
	for k, vs := range cw.Header() {
		w.Header()[k] = vs
	}
	w.WriteHeader(cw.statusCode)
	w.Write(cw.body.Bytes())
}
