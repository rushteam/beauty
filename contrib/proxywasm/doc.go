// Package proxywasm 实现完整的 Proxy-Wasm ABI v0.2.1，使 Higress/Envoy 生态的
// WASM 插件（用 proxy-wasm-go-sdk / proxy-wasm-rust-sdk / C++ SDK 编写）
// 可直接运行在 Beauty HTTP 中间件链中。
//
// # 核心概念
//
// Runtime — 持有 wazero 运行时 + 所有 host functions + 共享资源（SharedData/Metrics/Queue）。
// 一个进程通常创建一个 Runtime。
//
// Module — 编译后的 .wasm 字节码，可多次实例化。
//
// HTTPFilter — 标准 func(http.Handler) http.Handler 中间件。每个请求从 Pool 获取一个
// 已初始化的实例，依次调用 Proxy-Wasm 生命周期回调，处理完归还实例。
//
// # 快速使用
//
//	rt, _ := proxywasm.New(ctx)
//	mod, _ := rt.Compile(ctx, wasmBytes)
//	filter := proxywasm.HTTPFilter(mod, proxywasm.WithPluginConfig(cfg))
//	handler := filter(next)
//
// # 异步操作模型
//
// Proxy-Wasm 定义的异步操作（HTTP callout、gRPC call、foreign function）在本包中
// 采用"同步内联执行 + 延迟回调分发"模式：guest 发起异步操作时 host 同步执行并缓存结果，
// 待当前回调返回后立即分发 proxy_on_http_call_response 等回调。
// 通过 WithDispatcher 可注入自定义的 HTTP/gRPC 客户端实现。
//
// # ABI 完整性
//
// 所有 Proxy-Wasm v0.2.1 host functions 均已实现（日志、时间、定时器、header maps、
// buffers、流控、属性、HTTP callout、gRPC、shared data、shared queue、metrics、
// foreign function）。WASI 同时支持 wasi_snapshot_preview1 和 wasi_unstable 模块名。
//
// # 扩展点
//
//   - WithDispatcher(d) — 注入自定义 HTTP/gRPC callout 执行器
//   - WithForeignFunction(name, fn) — 注册宿主侧扩展函数
//   - WithObserver(fn) — 监听拦截/放行/错误事件
//   - rt.MetricsSnapshot() — 导出 guest 定义的所有指标
//
// 本包作为独立 Go 模块发布（github.com/rushteam/beauty/contrib/proxywasm），
// 基于 wazero（纯 Go、零 CGo），不依赖 beauty 核心。
package proxywasm
