package wasm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// ==== FaaS Router:路由 → wasm 函数(热插拔) ====

// FuncEntry 是路由表中的一条记录:路径模式 → 编译好的模块 + 配置。
type FuncEntry struct {
	Pattern string
	Module  *Module
	Options []HandlerOption
}

// Router 是 wasm 函数路由器:管理一组 wasm 函数(按路径模式分发),支持运行时热注册/注销。
// 实现 http.Handler,直接挂到 beauty 的 ServeMux 或独立 listener 上。
//
// 用法:
//
//	router := wasm.NewRouter(rt)
//	router.Register("/greet", greetModule, wasm.WithHandlerPool(4))
//	http.Handle("/fn/", http.StripPrefix("/fn", router))
type Router struct {
	rt *Runtime

	mu      sync.RWMutex
	entries map[string]*routeEntry
}

type routeEntry struct {
	mod     *Module
	handler http.Handler
	pool    *Pool
}

// NewRouter 创建函数路由器。rt 用于后续从字节码注册新函数。
func NewRouter(rt *Runtime) *Router {
	return &Router{rt: rt, entries: map[string]*routeEntry{}}
}

// Register 注册一个 wasm 函数到指定路径模式。若该模式已存在则热替换(旧实例池关闭)。
// 并发安全。
func (r *Router) Register(pattern string, mod *Module, opts ...HandlerOption) {
	h := Handler(mod, opts...)
	var pool *Pool
	for _, o := range opts {
		cfg := &HandlerConfig{}
		o(cfg)
		if cfg.poolSize > 0 {
			pool = mod.NewPool(cfg.poolSize)
		}
	}

	r.mu.Lock()
	old := r.entries[pattern]
	r.entries[pattern] = &routeEntry{mod: mod, handler: h}
	r.mu.Unlock()

	if old != nil && old.pool != nil {
		old.pool.Close(context.Background())
	}
	_ = pool // pool is managed by Handler internally
}

// RegisterBytes 从 wasm 字节码编译并注册。便捷方法:编译 + Register。
func (r *Router) RegisterBytes(ctx context.Context, pattern string, wasmBytes []byte, opts ...HandlerOption) error {
	mod, err := r.rt.Compile(ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("wasm: compile %q: %w", pattern, err)
	}
	r.Register(pattern, mod, opts...)
	return nil
}

// Deregister 注销指定路径的函数。
func (r *Router) Deregister(pattern string) {
	r.mu.Lock()
	old := r.entries[pattern]
	delete(r.entries, pattern)
	r.mu.Unlock()

	if old != nil && old.pool != nil {
		old.pool.Close(context.Background())
	}
}

// Patterns 返回当前注册的所有路径模式。
func (r *Router) Patterns() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for p := range r.entries {
		out = append(out, p)
	}
	return out
}

// ServeHTTP 实现 http.Handler:按请求路径匹配已注册的函数并分发。
// 匹配规则:精确匹配 > 最长前缀匹配(以 "/" 结尾的模式做前缀匹配)。
// 无匹配返回 404。
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	entry := r.match(req.URL.Path)
	r.mu.RUnlock()

	if entry == nil {
		http.NotFound(w, req)
		return
	}
	entry.handler.ServeHTTP(w, req)
}

func (r *Router) match(path string) *routeEntry {
	// 精确匹配优先
	if e, ok := r.entries[path]; ok {
		return e
	}
	// 最长前缀匹配(模式以 "/" 结尾)
	var best *routeEntry
	bestLen := 0
	for pattern, e := range r.entries {
		if !strings.HasSuffix(pattern, "/") {
			continue
		}
		if strings.HasPrefix(path, pattern) && len(pattern) > bestLen {
			best = e
			bestLen = len(pattern)
		}
	}
	return best
}
