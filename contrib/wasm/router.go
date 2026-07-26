package wasm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// ==== FaaS Router:路由 → wasm 函数(热插拔) ====

// FuncEntry 是路由表中的一条记录:路径模式 → 编译好的模块 + 配置。
type FuncEntry struct {
	Pattern string
	Module  *Module
	Options []HandlerOption
}

// RouterStats 是 Router 的瞬时/累计指标快照(接监控用)。
type RouterStats struct {
	Functions int   // 当前注册的路径数
	Hits      int64 // 命中已注册函数的请求数
	Misses    int64 // 未匹配(404)请求数
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

	hits   atomic.Int64
	misses atomic.Int64
}

type routeEntry struct {
	mod     *Module
	handler http.Handler
}

// NewRouter 创建函数路由器。rt 用于后续从字节码注册新函数。
func NewRouter(rt *Runtime) *Router {
	return &Router{rt: rt, entries: map[string]*routeEntry{}}
}

// Register 注册一个 wasm 函数到指定路径模式。若该模式已存在则热替换。
// 并发安全。
func (r *Router) Register(pattern string, mod *Module, opts ...HandlerOption) {
	h := Handler(mod, opts...)
	r.mu.Lock()
	r.entries[pattern] = &routeEntry{mod: mod, handler: h}
	r.mu.Unlock()
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
	delete(r.entries, pattern)
	r.mu.Unlock()
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

// Stats 返回当前指标快照(Functions 为瞬时值;Hits/Misses 为累计计数,进程不清零)。
func (r *Router) Stats() RouterStats {
	r.mu.RLock()
	n := len(r.entries)
	r.mu.RUnlock()
	return RouterStats{
		Functions: n,
		Hits:      r.hits.Load(),
		Misses:    r.misses.Load(),
	}
}

// ServeHTTP 实现 http.Handler:按请求路径匹配已注册的函数并分发。
// 匹配规则:精确匹配 > 最长前缀匹配(以 "/" 结尾的模式做前缀匹配)。
// 无匹配返回 404。
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	entry := r.match(req.URL.Path)
	r.mu.RUnlock()

	if entry == nil {
		r.misses.Add(1)
		http.NotFound(w, req)
		return
	}
	r.hits.Add(1)
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
