# contrib/proxywasm — Proxy-Wasm ABI v0.2.1 完整运行时

基于 **wazero**（纯 Go、零 CGo）实现完整的 Proxy-Wasm ABI v0.2.1，
使 Higress/Envoy 生态的 WASM 插件（用 proxy-wasm-go-sdk / proxy-wasm-rust-sdk 编写）
**无需修改**即可作为 Beauty HTTP 中间件运行。

```bash
go get github.com/rushteam/beauty/contrib/proxywasm@latest
```

---

## 一句话说明

> **把任何符合 Proxy-Wasm 标准的 .wasm 插件加载为 `func(http.Handler) http.Handler` 中间件。**

适用场景：复用已有的 Envoy/Higress/MOSN 生态 WASM 插件（认证、限流、header 改写、
body 修改、WAF 规则等），无需移植代码，直接在 Beauty 框架运行。

---

## 快速开始

```go
package main

import (
    "context"
    "net/http"
    "os"
    "time"

    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/contrib/proxywasm"
    "github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
    ctx := context.Background()

    // 1. 创建运行时
    rt, err := proxywasm.New(ctx,
        proxywasm.WithMemoryLimitPages(32),   // 每实例最大 2MB 内存
        proxywasm.WithLogLevel(proxywasm.LogInfo),
    )
    if err != nil {
        panic(err)
    }
    defer rt.Close(ctx)

    // 2. 编译 wasm 插件
    wasmBytes, _ := os.ReadFile("plugin.wasm")
    mod, err := rt.Compile(ctx, wasmBytes)
    if err != nil {
        panic(err)
    }

    // 3. 创建 HTTP 中间件
    filter := proxywasm.HTTPFilter(mod,
        proxywasm.WithPluginConfig([]byte(`{"block_urls": ["/admin"]}`)),
        proxywasm.WithPoolSize(16),
        proxywasm.WithTimeout(200*time.Millisecond),
    )

    // 4. 挂载到 Beauty
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("hello"))
    })
    beauty.New(
        beauty.WithWebServer(":8080", mux,
            webserver.WithMiddleware(filter),
        ),
    ).Start(ctx)
}
```

---

## 与 contrib/wasm 的关系

| | `contrib/wasm` | `contrib/proxywasm` |
|---|---|---|
| ABI | 自定义 JSON（`alloc`/`handle`） | Proxy-Wasm v0.2.1 标准 |
| 兼容性 | Beauty 专用 guest | Higress/Envoy/MOSN 生态插件 |
| 生命周期 | 无状态（每请求或池化） | 有状态（VM→Plugin→Stream 上下文） |
| 适用场景 | 轻量自研插件 | 复用已有 Envoy Wasm 插件 |

两者完全独立、共存，按需选用。

---

## API 参考

### Runtime（运行时）

```go
// 创建运行时（注册所有 host functions + WASI）
rt, err := proxywasm.New(ctx, opts...)

// 编译 .wasm 字节码（编译一次，实例化多次）
mod, err := rt.Compile(ctx, wasmBytes)

// 导出所有 metric 快照（用于接入 Prometheus/OTel）
snapshot := rt.MetricsSnapshot() // map[string]int64

// 关闭运行时
rt.Close(ctx)
```

### Runtime Options

| Option | 类型 | 说明 |
|--------|------|------|
| `WithMemoryLimitPages(n)` | `uint32` | 每实例线性内存页数上限（1页=64KiB），默认无限制 |
| `WithCacheDir(dir)` | `string` | 启用磁盘编译缓存，加速后续启动 |
| `WithLogger(l)` | `*slog.Logger` | 日志输出（proxy_log 和 WASI fd_write 都走这里） |
| `WithLogLevel(l)` | `LogLevel` | proxy_get_log_level 返回的级别 |
| `WithDispatcher(d)` | `Dispatcher` | 自定义异步操作分发器（HTTP/gRPC callout） |
| `WithForeignFunction(name, fn)` | `string, ForeignFunc` | 注册扩展函数（可调多次） |

### HTTPFilter（中间件）

```go
// 创建中间件（返回标准 func(http.Handler) http.Handler）
filter := proxywasm.HTTPFilter(mod, filterOpts...)

// 使用
handler := filter(nextHandler)
```

### Filter Options

| Option | 类型 | 说明 |
|--------|------|------|
| `WithVMConfig(data)` | `[]byte` | VM 配置（proxy_on_vm_start 时读取） |
| `WithPluginConfig(data)` | `[]byte` | 插件 JSON 配置（proxy_on_configure 时读取） |
| `WithPoolSize(n)` | `int` | 实例池大小（推荐 ≥ 并发请求数） |
| `WithWarm(n)` | `int` | 启动时预热实例数 |
| `WithTimeout(d)` | `time.Duration` | 执行超时（到期中断 guest 执行） |
| `WithFailOpen(b)` | `bool` | 出错时放行(true)还是 500(false，默认) |
| `WithFilterLogLevel(l)` | `LogLevel` | 运行时日志过滤级别 |
| `WithProperties(m)` | `map[string][]byte` | 注入自定义 properties |
| `WithObserver(fn)` | `func(FilterEvent)` | 可观测回调（拦截/放行/错误事件） |

---

## 异步操作（HTTP Callout / gRPC / Foreign Function）

Proxy-Wasm 允许 guest 插件在处理请求时发起外部调用。本实现采用
**"同步内联执行 + 延迟回调分发"** 模式：

```
Guest 调用 proxy_http_call(upstream, headers, body, timeout)
    │
    ▼  Host 同步发起 HTTP 请求（阻塞当前 goroutine）
    │
    ▼  结果暂存，返回 callout token 给 guest
    │
    ▼  当前 guest 回调(如 proxy_on_request_headers)返回
    │
    ▼  Host 分发 proxy_on_http_call_response(ctx, token, ...)
    │
    ▼  继续正常请求处理
```

### 自定义 Dispatcher

默认使用 `net/http.Client` 发起 HTTP callout（gRPC 需自定义）：

```go
type MyDispatcher struct {
    httpClient *http.Client
    grpcPool   *grpc.ClientConnPool
}

func (d *MyDispatcher) HTTPCall(ctx context.Context, req *proxywasm.HTTPCallRequest) (*proxywasm.HTTPCallResponse, error) {
    // 自定义路由逻辑：根据 req.Upstream 选择后端
    // ...
}

func (d *MyDispatcher) GRPCCall(ctx context.Context, req *proxywasm.GRPCCallRequest) (*proxywasm.GRPCCallResponse, error) {
    // 对接内部 gRPC mesh
    // ...
}

rt, _ := proxywasm.New(ctx,
    proxywasm.WithDispatcher(&MyDispatcher{...}),
)
```

### Foreign Function（自定义扩展）

注册宿主侧函数，guest 通过 `proxy_call_foreign_function` 调用：

```go
rt, _ := proxywasm.New(ctx,
    proxywasm.WithForeignFunction("lookup_user", func(args []byte) ([]byte, error) {
        userID := string(args)
        user, err := userService.Get(userID)
        if err != nil {
            return nil, err
        }
        return json.Marshal(user)
    }),
    proxywasm.WithForeignFunction("get_config", func(args []byte) ([]byte, error) {
        return configStore.Get(string(args))
    }),
)
```

---

## Shared Data & Shared Queue

### Shared Data（跨实例 KV + CAS）

所有同一 Runtime 的实例共享一个 key-value 存储，支持 Compare-And-Swap：

- `proxy_get_shared_data(key)` → (value, cas_token)
- `proxy_set_shared_data(key, value, cas)` → 成功/CAS_MISMATCH

适用于：限流计数器、全局开关、配置缓存。

### Shared Queue（跨实例消息队列）

FIFO 消息队列，按 (vm_id, name) 注册：

- `proxy_register_shared_queue(name)` → queue_id
- `proxy_resolve_shared_queue(vm_id, name)` → queue_id
- `proxy_enqueue_shared_queue(queue_id, data)`
- `proxy_dequeue_shared_queue(queue_id)` → data

适用于：异步日志、采样、跨插件通信。

---

## Metrics（指标）

Guest 插件可定义和操作指标，宿主通过 `rt.MetricsSnapshot()` 导出：

```go
// Guest 侧 (proxy-wasm-go-sdk):
// counter, _ := proxywasm.DefineCounterMetric("my_plugin_requests_total")
// counter.Increment(1)

// Host 侧:
go func() {
    for range time.Tick(10 * time.Second) {
        for name, value := range rt.MetricsSnapshot() {
            prometheus.NewGauge(...).Set(float64(value))
        }
    }
}()
```

支持 Counter / Gauge / Histogram 三种类型。

---

## Guest 生命周期

```
┌─ 实例初始化（一次） ─────────────────────────────────────┐
│  _start / _initialize                                    │
│  proxy_on_vm_start(root_ctx, vm_config_size)             │
│  proxy_on_context_create(plugin_ctx, root_ctx)           │
│  proxy_on_configure(plugin_ctx, plugin_config_size)      │
└──────────────────────── 实例就绪，可复用处理多个请求 ──────┘

┌─ 每个 HTTP 请求 ────────────────────────────────────────┐
│  proxy_on_context_create(stream_ctx, plugin_ctx)         │
│  proxy_on_request_headers(stream_ctx, num, end_of_stream)│
│  proxy_on_request_body(stream_ctx, body_size, true)      │
│  proxy_on_request_trailers(stream_ctx, num_trailers)     │
│  ─── [dispatch pending callouts] ───                     │
│  ─── [next.ServeHTTP - 下游处理] ───                     │
│  proxy_on_response_headers(stream_ctx, num, end_of_stream)│
│  proxy_on_response_body(stream_ctx, body_size, true)     │
│  proxy_on_response_trailers(stream_ctx, num_trailers)    │
│  proxy_on_log(stream_ctx)                                │
│  proxy_on_done(stream_ctx)                               │
│  proxy_on_delete(stream_ctx)                             │
└──────────────────────────────────────────────────────────┘

┌─ Tick 定时器（如果 guest 设置了 tick_period） ──────────┐
│  proxy_on_tick(root_ctx) — 在每次请求开始时检查并触发    │
└──────────────────────────────────────────────────────────┘
```

---

## ABI 完整实现清单

### Host Functions（全部已实现）

| 分类 | 函数 | 状态 |
|------|------|------|
| **日志** | `proxy_log`, `proxy_get_log_level` | ✅ |
| **时间** | `proxy_get_current_time_nanoseconds` | ✅ |
| **定时器** | `proxy_set_tick_period_milliseconds` | ✅ |
| **Header Maps** | `get_pairs`, `set_pairs`, `get_value`, `add`, `replace`, `remove`, `get_size` | ✅ |
| **Buffers** | `get_buffer_bytes`, `set_buffer_bytes`, `get_buffer_status` | ✅ |
| **流控** | `continue_stream`, `close_stream`, `send_local_response` | ✅ |
| **上下文** | `set_effective_context`, `done` | ✅ |
| **属性** | `get_property`, `set_property` | ✅ |
| **HTTP Callout** | `proxy_http_call` → `proxy_on_http_call_response` | ✅ |
| **gRPC** | `grpc_call`, `grpc_stream`, `grpc_send`, `grpc_cancel`, `grpc_close` | ✅ |
| **Shared Data** | `get_shared_data`, `set_shared_data`（CAS 语义） | ✅ |
| **Shared Queue** | `register`, `resolve`, `enqueue`, `dequeue` | ✅ |
| **Metrics** | `define_metric`, `record_metric`, `increment_metric`, `get_metric` | ✅ |
| **Foreign Function** | `proxy_call_foreign_function` | ✅ |

### Guest Callbacks（全部已支持）

| 回调 | 说明 |
|------|------|
| `proxy_on_vm_start` | VM 启动 |
| `proxy_on_configure` | 插件配置 |
| `proxy_on_context_create` | 创建上下文 |
| `proxy_on_request_headers` | 请求头 |
| `proxy_on_request_body` | 请求体 |
| `proxy_on_request_trailers` | 请求 trailers |
| `proxy_on_response_headers` | 响应头 |
| `proxy_on_response_body` | 响应体 |
| `proxy_on_response_trailers` | 响应 trailers |
| `proxy_on_http_call_response` | HTTP callout 响应 |
| `proxy_on_grpc_receive_initial_metadata` | gRPC 初始元数据 |
| `proxy_on_grpc_receive` | gRPC 消息 |
| `proxy_on_grpc_close` | gRPC 关闭 |
| `proxy_on_tick` | 定时器触发 |
| `proxy_on_log` | 日志阶段 |
| `proxy_on_done` | 完成 |
| `proxy_on_delete` | 删除上下文 |

### WASI 支持

完整实现 `wasi_snapshot_preview1` 和 `wasi_unstable` 两个模块名：
- I/O: `fd_write`→slog, `fd_read`/`fd_close`/`fd_seek` (stub)
- 时间: `clock_time_get`, `clock_res_get`
- 随机: `random_get`
- 环境: `environ_sizes_get`, `environ_get`, `args_sizes_get`, `args_get`
- 进程: `proc_exit` (no-op), `sched_yield`
- 文件系统: 全部 path_*/fd_* 函数（安全 stub，返回 ENOENT/EBADF）
- 网络: sock_* (ENOSYS stub)

---

## 架构设计

```
┌──────────────────────────────────────────────────────────┐
│                    用户代码                                │
│  beauty.New(beauty.WithWebServer(":8080", mux,           │
│      webserver.WithMiddleware(proxywasm.HTTPFilter(mod)), │
│  ))                                                       │
└─────────────────────┬────────────────────────────────────┘
                      │
┌─────────────────────▼────────────────────────────────────┐
│              HTTPFilter (middleware.go)                    │
│  - 从 Pool 获取实例                                       │
│  - 调用 handleHTTP (请求阶段)                             │
│  - 调用 next.ServeHTTP (下游)                             │
│  - 调用 handleResponse (响应阶段)                         │
│  - 归还实例到 Pool                                        │
└─────────────────────┬────────────────────────────────────┘
                      │
┌─────────────────────▼────────────────────────────────────┐
│                   Pool (pool.go)                           │
│  - 有状态实例池（已完成 VM start + Plugin configure）     │
│  - 空闲复用 / 按需创建 / 超出容量丢弃                     │
└─────────────────────┬────────────────────────────────────┘
                      │
┌─────────────────────▼────────────────────────────────────┐
│               Instance (instance.go)                      │
│  - initInstance: VM→Plugin 初始化                         │
│  - handleHTTP: request headers/body/trailers             │
│  - dispatchPendingCallouts: 异步操作回调分发              │
│  - handleResponse: response headers/body/trailers        │
│  - finishStream: log→done→delete                         │
│  - firePendingTicks: tick 定时器                          │
└─────────────────────┬────────────────────────────────────┘
                      │
┌─────────────────────▼────────────────────────────────────┐
│            Host Functions (hostcalls.go)                   │
│  - 所有 proxy_* 函数实现                                  │
│  - 通过 context.Value 获取 pluginState                    │
└───────────┬──────────┬──────────┬────────────────────────┘
            │          │          │
     ┌──────▼───┐ ┌────▼────┐ ┌──▼──────────┐
     │SharedData│ │ Metrics │ │ Dispatcher  │
     │(CAS KV)  │ │(atomic) │ │(HTTP/gRPC)  │
     └──────────┘ └─────────┘ └─────────────┘
```

---

## 常见问题

### Q: 支持哪些语言编写的 wasm 插件？

任何使用 proxy-wasm SDK 编译的插件：
- Go（proxy-wasm-go-sdk）
- Rust（proxy-wasm-rust-sdk）
- C++（proxy-wasm-cpp-sdk）
- AssemblyScript（proxy-wasm-assemblyscript-sdk）

### Q: 性能如何？

- wazero 编译器模式，接近原生性能
- 实例池避免重复初始化开销
- 可通过 `WithCacheDir` 启用磁盘编译缓存
- 零 CGo，无 GC 压力

### Q: 插件需要发 HTTP 请求怎么办？

插件调用 `proxy_http_call` 时，默认使用 `net/http.Client` 同步发起请求。
如需对接服务网格或自定义路由，注入 `WithDispatcher(myDispatcher)` 即可。

### Q: 如何调试插件？

1. 设置 `WithLogLevel(proxywasm.LogTrace)` 查看所有 proxy_log 输出
2. 使用 `WithObserver` 监听拦截/放行事件
3. Guest 的 stdout/stderr (fd_write) 会自动路由到 slog

### Q: 与 Higress 网关的关系？

本包让你在 **不依赖 Envoy/Higress 网关** 的情况下运行 Proxy-Wasm 插件。
适合：
- 开发阶段本地测试 Higress 插件
- 微服务内嵌插件逻辑（无需网关）
- 逐步迁移到 Higress 时保持插件复用

---

## 设计原则

1. **机制而非策略** — 加载哪个 wasm、配置什么、池多大，都是使用方决定
2. **同步模型** — 利用 Go goroutine-per-request，将 Proxy-Wasm 的异步事件映射为同步调用
3. **完整 ABI** — v0.2.1 所有 host function 均已实现，不会因 import 缺失导致加载失败
4. **零 CGo** — wazero 纯 Go 实现，交叉编译友好
5. **可扩展** — Dispatcher / ForeignFunction / Observer 三个注入点覆盖大部分定制需求
