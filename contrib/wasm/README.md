# contrib/wasm —— WebAssembly 插件运行时(独立模块)

用 **wazero**(纯 Go、零 CGo)把业务逻辑/策略写成**沙箱化、可热插拔**的 wasm 模块,挂到 beauty 的
扩展点(HTTP 中间件、webhook、策略…)。guest 可用 TinyGo / Rust / Go(`//go:wasmexport`)等编写。

```bash
go get github.com/rushteam/beauty/contrib/wasm@latest
```

## 安全边界(机制而非策略)

默认**不启用 WASI**:guest 没有文件、网络、环境变量、时钟——只能做纯计算,并通过你**显式授予**的
host functions 与外界交互。资源上限:**内存**用 `WithMemoryLimitPages`,**执行时间(CPU)**用中间件的
`WithTimeout`——超时会**中断** guest 执行(Runtime 默认已开启 `CloseOnContextDone`),挡住死循环。
要放开某项能力,由你显式加。

## Runtime:编译 + 调用 + host functions

```go
ctx := context.Background()
rt, _ := wasm.New(ctx,
    wasm.WithMemoryLimitPages(16), // 每实例最多 16*64KiB
    wasm.WithHostFunc("env", "log", func(_ context.Context, m api.Module, ptr, n uint32) {
        buf, _ := m.Memory().Read(ptr, n) // 显式授予 guest 的“打日志”能力
        log.Print(string(buf))
    }),
)
defer rt.Close(ctx)

// 内置 host functions(仍需显式授予):
//   wasm.WithLog(logger)  → env.log(ptr,len)     guest 打日志
//   wasm.WithClock()      → env.now_unix_milli()  guest 读时间(默认无 WASI 时钟)
mod, _ := rt.Compile(ctx, wasmBytes) // 编译一次,复用
inst, _ := mod.Instantiate(ctx)      // 每次得到独立内存的实例(非并发安全)
defer inst.Close(ctx)
res, _ := inst.Call(ctx, "add", api.EncodeI32(2), api.EncodeI32(3))
```

## HTTP 中间件即 wasm 模块

把请求元数据交给 guest 的 `handle`,按返回的**决策**放行或拦截:

```go
mod, _ := rt.Compile(ctx, filterWasm)
mux := http.NewServeMux()
handler := wasm.Middleware(mod,
    wasm.WithTimeout(100*time.Millisecond), // 执行超时,中断死循环(超时按 fail 策略处理)
    wasm.WithPool(16),                       // 实例池复用(降低实例化开销;省略则每请求新建)
    wasm.WithBody(64*1024),                  // 可选:让 guest 看到请求体前 64KB(默认不读,零成本)
    wasm.WithObserver(func(e wasm.Event) {   // 可观测:接指标/日志/追踪(不绑具体实现)
        // e.Action("next"/"deny"/"error") / e.Err / e.Duration
    }),
)(yourHandler)
// wasm.WithFailOpen(true)  出错/超时时放行(默认 fail-closed 返回 500,适合鉴权/WAF)
// WithBody:guest 只见前 maxBytes(Request.Body 为 base64,超长置 BodyTruncated=true),
//   下游始终收到完整 body;不启用则完全不读 body。
```

### guest ABI

guest 需实现:

- 导出 `memory`;
- `alloc(i32 size) -> i32 ptr`:分配 size 字节,host 把**请求 JSON**写在返回地址;
- `handle(i32 reqPtr, i32 reqLen) -> i64`:返回打包地址 `(respPtr<<32) | respLen`,host 从该处读**决策 JSON**。

请求 / 决策结构(即本包的 `wasm.Request` / `wasm.Decision`):

```jsonc
// 传入 handle 的 Request
{ "method": "GET", "path": "/x", "query": "a=1", "headers": {"X-Token": "..."} }
// handle 返回的 Decision
{ "action": "next" }                                   // 放行
{ "action": "next",                                    // 放行 + 改写请求头(Set 覆盖 / Add 追加 / Remove 删除)
  "setRequestHeaders": {"X-User": "alice"},
  "addRequestHeaders": {"X-Trace": ["a", "b"]},
  "removeRequestHeaders": ["X-Secret"] }
{ "action": "deny", "status": 403, "body": "blocked",  // 拦截
  "headers": {"X-Reason": "..."} }
```

### 用 TinyGo 写一个 guest(示例)

```go
//go:build tinygo

package main

import "encoding/json"

var buf [4096]byte // 简单静态缓冲(演示)

//export alloc
func alloc(size int32) *byte { return &buf[0] }

//export handle
func handle(ptr *byte, n int32) int64 {
    req := buf[:n]
    var r struct{ Path string `json:"path"` }
    _ = json.Unmarshal(req, &r)
    var dec []byte
    if r.Path == "/admin" {
        dec = []byte(`{"action":"deny","status":403,"body":"nope"}`)
    } else {
        dec = []byte(`{"action":"next"}`)
    }
    copy(buf[2048:], dec)
    return (int64(2048) << 32) | int64(len(dec))
}

func main() {}
```

编译:`tinygo build -o filter.wasm -target=wasi ./guest`(Rust 用 `wasm32-unknown-unknown`;
Go 用 `GOOS=wasip1 GOARCH=wasm` + `//go:wasmexport`)。真实 guest 应做健壮的分配与边界检查。

## 边界

选加载哪个模块、授予哪些 host 能力、内存上限、出错 fail-open/closed 都是 policy。本包只做
"编译 + 沙箱 + ABI + beauty 挂载"。单测用**手工编码的极小 wasm**(无需工具链/WASI)覆盖
Runtime / host func / 内存 / 中间件 / FaaS 全链路。

## FaaS-lite:wasm 函数即 HTTP Handler

与 Middleware 共享同一 guest ABI(`alloc`/`handle`),但 guest 输出的不是决策(Decision),
而是**完整 HTTP 响应**(Response)——直接作为终端处理器,无需下游 handler。

### 单函数 Handler

```go
mod, _ := rt.Compile(ctx, greetWasm)
h := wasm.Handler(mod,
    wasm.WithHandlerPool(8),                   // 实例池(复用降低开销)
    wasm.WithHandlerTimeout(5*time.Second),    // 执行超时
    wasm.WithHandlerBody(4096),                // guest 可见请求体(前 4KB)
)
http.Handle("/greet", h)
```

### Router:多函数路由 + 热插拔

```go
router := wasm.NewRouter(rt)

// 从字节码注册(一步编译+挂载)
router.RegisterBytes(ctx, "/greet", greetWasm, wasm.WithHandlerPool(8))
router.RegisterBytes(ctx, "/echo", echoWasm)

// 前缀匹配:以 "/" 结尾的模式匹配所有子路径
router.RegisterBytes(ctx, "/api/", apiWasm)
// GET /api/users → 匹配 "/api/"
// GET /api/special → 若注册了精确路径则优先精确匹配

// 挂到 ServeMux(StripPrefix 去掉公共前缀)
http.Handle("/fn/", http.StripPrefix("/fn", router))

// 热更新:同路径再次 Register 即替换(旧实例池自动回收)
router.RegisterBytes(ctx, "/greet", greetV2Wasm)

// 注销
router.Deregister("/greet")

// 查看当前注册的所有路径
patterns := router.Patterns() // ["/echo", "/api/"]
```

### guest Response 格式

guest 的 `handle` 返回 Response JSON(而非 Decision):

```jsonc
{
  "status": 200,                          // HTTP 状态码(0 或省略按 200)
  "headers": {"Content-Type": "text/plain"}, // 响应头(可选)
  "body": "Hello, world!"                 // 响应体(可选)
}
```

### 用 Go 写 FaaS guest

```go
package main

import (
    "encoding/json"
    "unsafe"
)

type Request struct {
    Method  string            `json:"method"`
    Path    string            `json:"path"`
    Query   string            `json:"query"`
    Headers map[string]string `json:"headers"`
    Body    string            `json:"body"`
}

type Response struct {
    Status  int               `json:"status"`
    Headers map[string]string `json:"headers,omitempty"`
    Body    string            `json:"body,omitempty"`
}

var buf [65536]byte

//go:wasmexport alloc
func alloc(size int32) int32 {
    return int32(uintptr(unsafe.Pointer(&buf[0])))
}

//go:wasmexport handle
func handle(ptr, length int32) int64 {
    input := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
    var req Request
    json.Unmarshal(input, &req)

    resp := Response{
        Status:  200,
        Headers: map[string]string{"Content-Type": "application/json"},
        Body:    `{"hello":"` + req.Path + `"}`,
    }
    out, _ := json.Marshal(resp)
    copy(buf[:], out)

    outPtr := int64(uintptr(unsafe.Pointer(&buf[0])))
    return (outPtr << 32) | int64(len(out))
}

func main() {}
```

编译:`GOOS=wasip1 GOARCH=wasm go build -o fn.wasm .`

### Middleware vs Handler 对照

| | Middleware | Handler (FaaS) |
|---|---|---|
| 角色 | 中间件(过滤/拦截) | 终端处理器(生产响应) |
| guest 输出 | `Decision`(next/deny) | `Response`(status+headers+body) |
| 挂载 | `wasm.Middleware(mod)(next)` | `wasm.Handler(mod)` 或 `Router` |
| 场景 | WAF、鉴权、限流、改写 | Serverless 函数、计算端点、webhook |
