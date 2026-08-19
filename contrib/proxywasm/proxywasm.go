package proxywasm

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tetratelabs/wazero"
)

// Runtime 是 Proxy-Wasm 运行时(持有 wazero.Runtime 与已注册的 host functions)。
// 并发安全: 编译/实例化可并发;单个 instance 不可并发(由 pool 保护)。
type Runtime struct {
	rt           wazero.Runtime
	cache        wazero.CompilationCache
	logger       *slog.Logger
	sharedData   *sharedDataStore
	metrics      *metricStore
	sharedQueue  *SharedQueue
	foreignFuncs *ForeignFuncRegistry
	dispatcher   Dispatcher
}

// Option 配置 Runtime。
type Option func(*runtimeConfig)

type runtimeConfig struct {
	memoryLimitPages uint32
	cacheDir         string
	logger           *slog.Logger
	logLevel         LogLevel
	dispatcher       Dispatcher
	foreignFuncs     map[string]ForeignFunc
}

// WithMemoryLimitPages 限制每个实例的线性内存页数(1 页 = 64KiB)。
func WithMemoryLimitPages(pages uint32) Option {
	return func(c *runtimeConfig) { c.memoryLimitPages = pages }
}

// WithCacheDir 启用磁盘编译缓存。
func WithCacheDir(dir string) Option {
	return func(c *runtimeConfig) { c.cacheDir = dir }
}

// WithLogger 设置日志输出(proxy_log 和 WASI fd_write 都走这里)。
func WithLogger(l *slog.Logger) Option {
	return func(c *runtimeConfig) { c.logger = l }
}

// WithLogLevel 设置 proxy_get_log_level 返回的级别(低于此级别的日志不输出)。
func WithLogLevel(l LogLevel) Option {
	return func(c *runtimeConfig) { c.logLevel = l }
}

// WithDispatcher 注入自定义异步操作分发器(HTTP callout, gRPC call)。
// 不设置则使用 DefaultDispatcher (基于 net/http.Client)。
func WithDispatcher(d Dispatcher) Option {
	return func(c *runtimeConfig) { c.dispatcher = d }
}

// WithForeignFunction 注册一个 foreign function (可被 guest 通过 proxy_call_foreign_function 调用)。
func WithForeignFunction(name string, fn ForeignFunc) Option {
	return func(c *runtimeConfig) {
		if c.foreignFuncs == nil {
			c.foreignFuncs = make(map[string]ForeignFunc)
		}
		c.foreignFuncs[name] = fn
	}
}

// New 创建 Proxy-Wasm 运行时。注册所有 proxy_* host functions 和最小 WASI 子集。
func New(ctx context.Context, opts ...Option) (*Runtime, error) {
	cfg := runtimeConfig{
		logger:   slog.Default(),
		logLevel: LogInfo,
	}
	for _, o := range opts {
		o(&cfg)
	}

	rtCfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	if cfg.memoryLimitPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(cfg.memoryLimitPages)
	}

	var cache wazero.CompilationCache
	if cfg.cacheDir != "" {
		c, err := wazero.NewCompilationCacheWithDir(cfg.cacheDir)
		if err != nil {
			return nil, fmt.Errorf("proxywasm: cache dir %q: %w", cfg.cacheDir, err)
		}
		cache = c
		rtCfg = rtCfg.WithCompilationCache(cache)
	}

	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)

	// 注册 env 模块(proxy_* host functions)
	envBuilder := newHostModuleBuilder()
	registerHostFunctions(envBuilder, cfg.logger)

	b := rt.NewHostModuleBuilder("env")
	for name, fn := range envBuilder.fns {
		b = b.NewFunctionBuilder().WithFunc(fn).Export(name).(wazero.HostModuleBuilder)
	}
	if _, err := b.Instantiate(ctx); err != nil {
		_ = rt.Close(ctx)
		if cache != nil {
			_ = cache.Close(ctx)
		}
		return nil, fmt.Errorf("proxywasm: register env module: %w", err)
	}

	// 注册 wasi_snapshot_preview1 模块
	wasiBuilder := newHostModuleBuilder()
	registerWASI(wasiBuilder, cfg.logger)

	wb := rt.NewHostModuleBuilder("wasi_snapshot_preview1")
	for name, fn := range wasiBuilder.fns {
		wb = wb.NewFunctionBuilder().WithFunc(fn).Export(name).(wazero.HostModuleBuilder)
	}
	if _, err := wb.Instantiate(ctx); err != nil {
		_ = rt.Close(ctx)
		if cache != nil {
			_ = cache.Close(ctx)
		}
		return nil, fmt.Errorf("proxywasm: register wasi module: %w", err)
	}

	// 注册 wasi_unstable 模块(旧模块名兼容)
	wbu := rt.NewHostModuleBuilder("wasi_unstable")
	for name, fn := range wasiBuilder.fns {
		wbu = wbu.NewFunctionBuilder().WithFunc(fn).Export(name).(wazero.HostModuleBuilder)
	}
	if _, err := wbu.Instantiate(ctx); err != nil {
		_ = rt.Close(ctx)
		if cache != nil {
			_ = cache.Close(ctx)
		}
		return nil, fmt.Errorf("proxywasm: register wasi_unstable module: %w", err)
	}

	disp := cfg.dispatcher
	if disp == nil {
		disp = &DefaultDispatcher{}
	}

	ff := newForeignFuncRegistry()
	for name, fn := range cfg.foreignFuncs {
		ff.Register(name, fn)
	}

	return &Runtime{
		rt:           rt,
		cache:        cache,
		logger:       cfg.logger,
		sharedData:   newSharedDataStore(),
		metrics:      newMetricStore(),
		sharedQueue:  newSharedQueue(),
		foreignFuncs: ff,
		dispatcher:   disp,
	}, nil
}

// Close 关闭运行时。
func (r *Runtime) Close(ctx context.Context) error {
	err := r.rt.Close(ctx)
	if r.cache != nil {
		if e := r.cache.Close(ctx); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// MetricsSnapshot 返回所有已定义的 metric 名字和当前值（可用于导出到监控系统）。
func (r *Runtime) MetricsSnapshot() map[string]int64 {
	return r.metrics.Snapshot()
}

// Module 是编译后的 Proxy-Wasm 模块。编译一次,可多次创建实例。
type Module struct {
	rt       *Runtime
	compiled wazero.CompiledModule
}

// Compile 编译 wasm 字节码。
func (r *Runtime) Compile(ctx context.Context, wasmBinary []byte) (*Module, error) {
	c, err := r.rt.CompileModule(ctx, wasmBinary)
	if err != nil {
		return nil, fmt.Errorf("proxywasm: compile: %w", err)
	}
	return &Module{rt: r, compiled: c}, nil
}

// instantiate 创建一个新的原始 wazero 模块实例。
func (m *Module) instantiate(ctx context.Context) (*instance, error) {
	mod, err := m.rt.rt.InstantiateModule(ctx, m.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("proxywasm: instantiate: %w", err)
	}
	return &instance{mod: mod, state: nil}, nil
}
