// Package wasm 是 beauty 的 WebAssembly 插件运行时:用 wazero(纯 Go、零 CGo)把业务逻辑/策略
// 写成**沙箱化、可热插拔**的 wasm 模块,挂到 beauty 的扩展点(HTTP 中间件、webhook、策略等)。
// 作为**独立 Go 模块**发布(github.com/rushteam/beauty/contrib/wasm)。
//
// 分层:
//   - Runtime:封装 wazero——编译模块、注册受控 host functions、按内存上限实例化;
//   - Module:编译后的模块(编译一次、多次实例化);
//   - Instance:一次实例(自带线性内存,非并发安全,用完 Close);
//   - middleware.go:在此之上的"HTTP 中间件即 wasm 模块"(见该文件)。
//
// 安全边界(机制而非策略):默认**不启用 WASI**,故 guest 无文件、无网络、无环境变量、无时钟——
// 只能做纯计算并通过你显式授予的 host functions 与外界交互;内存有上限。要放开某项能力,由你显式加。
package wasm

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Runtime 是 wasm 运行时(持有 wazero.Runtime 与已注册的 host functions)。并发安全:
// 编译/实例化可并发;单个 Instance 不可并发。用 New 构造,用完 Close。
type Runtime struct {
	rt wazero.Runtime
}

// Option 配置 Runtime。
type Option func(*config)

type config struct {
	memoryLimitPages uint32 // 每个实例的线性内存上限(页,1 页=64KiB);0=用 wazero 默认
	hostFuncs        []hostFunc
}

type hostFunc struct {
	module, name string
	fn           any
}

// WithMemoryLimitPages 限制每个实例的线性内存页数(1 页 = 64KiB)。防止 guest 撑爆内存。
func WithMemoryLimitPages(pages uint32) Option {
	return func(c *config) { c.memoryLimitPages = pages }
}

// WithHostFunc 注册一个 host function 供 guest import。fn 是普通 Go 函数,签名只能用
// wasm 数值类型(int32/uint32/int64/uint64/float32/float64),可选首参 context.Context、
// 次参 api.Module(用于读写 guest 内存)。这是你**显式授予 guest 的能力**。
//
//	WithHostFunc("env", "log", func(_ context.Context, m api.Module, ptr, n uint32) {
//	    buf, _ := m.Memory().Read(ptr, n)
//	    log.Print(string(buf))
//	})
func WithHostFunc(module, name string, fn any) Option {
	return func(c *config) { c.hostFuncs = append(c.hostFuncs, hostFunc{module, name, fn}) }
}

// New 创建运行时。默认不启用 WASI(无 fs/net/env/clock)。
func New(ctx context.Context, opts ...Option) (*Runtime, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	rtCfg := wazero.NewRuntimeConfig().
		// 允许用 context 取消/超时**中断执行**——挡住失控/恶意 guest 的死循环(CPU 上限)。
		// 有微小运行时开销,但对"跑不可信插件"是必需的。
		WithCloseOnContextDone(true)
	if cfg.memoryLimitPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(cfg.memoryLimitPages)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)

	// 注册 host functions(按 module 分组)。
	byModule := map[string][]hostFunc{}
	for _, h := range cfg.hostFuncs {
		byModule[h.module] = append(byModule[h.module], h)
	}
	for mod, fns := range byModule {
		b := rt.NewHostModuleBuilder(mod)
		for _, h := range fns {
			b = b.NewFunctionBuilder().WithFunc(h.fn).Export(h.name).(wazero.HostModuleBuilder)
		}
		if _, err := b.Instantiate(ctx); err != nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("wasm: 注册 host module %q: %w", mod, err)
		}
	}
	return &Runtime{rt: rt}, nil
}

// Close 关闭运行时(释放所有实例与编译缓存)。
func (r *Runtime) Close(ctx context.Context) error { return r.rt.Close(ctx) }

// Module 是编译后的 wasm 模块。编译一次,可多次 Instantiate(每次得到独立内存的实例)。
type Module struct {
	rt       *Runtime
	compiled wazero.CompiledModule
}

// Compile 编译 wasm 字节码(.wasm)。编译较慢,应在启动时做一次并复用。
func (r *Runtime) Compile(ctx context.Context, wasmBinary []byte) (*Module, error) {
	c, err := r.rt.CompileModule(ctx, wasmBinary)
	if err != nil {
		return nil, fmt.Errorf("wasm: 编译: %w", err)
	}
	return &Module{rt: r, compiled: c}, nil
}

// Instance 是模块的一次实例:自带线性内存与状态,**不可并发使用**。用完 Close。
type Instance struct {
	mod api.Module
}

// Instantiate 新建一个匿名实例(可反复调用得到互相隔离的实例,适合"每请求一个")。
func (m *Module) Instantiate(ctx context.Context) (*Instance, error) {
	// WithName("") 允许同一模块实例化多次(否则同名会冲突)。
	mod, err := m.rt.rt.InstantiateModule(ctx, m.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("wasm: 实例化: %w", err)
	}
	return &Instance{mod: mod}, nil
}

// Close 销毁实例。
func (i *Instance) Close(ctx context.Context) error { return i.mod.Close(ctx) }

// Call 调用一个导出函数,参数与返回都是原始 uint64(用 api.EncodeI32 等编码)。
// 找不到该导出函数时返回错误。
func (i *Instance) Call(ctx context.Context, name string, args ...uint64) ([]uint64, error) {
	fn := i.mod.ExportedFunction(name)
	if fn == nil {
		return nil, fmt.Errorf("wasm: 未导出函数 %q", name)
	}
	return fn.Call(ctx, args...)
}

// WriteTo 让 guest 分配 len(data) 字节(调用其导出的 allocFn,通常名为 "alloc"),把 data 写入
// guest 线性内存,返回写入起始地址。guest 必须导出 alloc(i32)->i32 且导出 memory。
func (i *Instance) WriteTo(ctx context.Context, allocFn string, data []byte) (uint32, error) {
	res, err := i.Call(ctx, allocFn, api.EncodeI32(int32(len(data))))
	if err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, fmt.Errorf("wasm: %s 未返回地址", allocFn)
	}
	ptr := api.DecodeU32(res[0])
	mem := i.mod.Memory()
	if mem == nil {
		return 0, fmt.Errorf("wasm: 模块未导出 memory")
	}
	if !mem.Write(ptr, data) {
		return 0, fmt.Errorf("wasm: 写内存越界(ptr=%d len=%d)", ptr, len(data))
	}
	return ptr, nil
}

// ReadBytes 从 guest 线性内存读取 [ptr, ptr+n)。
func (i *Instance) ReadBytes(ptr, n uint32) ([]byte, error) {
	mem := i.mod.Memory()
	if mem == nil {
		return nil, fmt.Errorf("wasm: 模块未导出 memory")
	}
	buf, ok := mem.Read(ptr, n)
	if !ok {
		return nil, fmt.Errorf("wasm: 读内存越界(ptr=%d len=%d)", ptr, n)
	}
	// Read 返回的是内存视图,复制一份避免被后续调用覆盖。
	out := make([]byte, len(buf))
	copy(out, buf)
	return out, nil
}
