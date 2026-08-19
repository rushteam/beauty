# WASM 方向 Roadmap

在 beauty 里引入 WebAssembly 的规划。统一运行时选型:**wazero**(纯 Go、零 CGo、可嵌入),
按"重依赖进 contrib、核心零负担"的约定,落在 `contrib/wasm`。原则同框架:**机制而非策略**
——框架提供"加载 + 沙箱 + ABI",具体规则/逻辑由用户的 wasm 模块承载;默认无文件/无网络,按需授权。

## Tier 1 —— wasm 插件运行时(地基)· 已落地(`contrib/wasm`)

`contrib/wasm`(基于 wazero):把业务逻辑/策略写成沙箱化、可热插拔的 wasm 模块,挂到 beauty 的
扩展点(`handler`/`middleware`/`governance`/`webhook`)。

- Runtime 封装:编译/实例化/调用导出函数、模块缓存;
- 受控 host functions(日志、KV、读取请求元数据…),能力按需授权;
- 内存上限(`WithMemoryLimitPages`)+ 执行超时/中断(`WithTimeout` + `CloseOnContextDone`)+ 默认关闭 WASI 的文件与网络;
- 高层封装:**HTTP 中间件即 wasm 模块**——请求元数据 → wasm `handle` → 决策(放行/拒绝/改写请求头/状态码);
- `pkg/api/handler.WithMiddleware` 通用口,声明式绑定 wasm(核心零 contrib 依赖)。

已补打磨:实例池(`WithPool`)+预热(`WithWarm`/`Pool.Warm`)、磁盘编译缓存(`WithCacheDir`)、
内置 host functions(`WithLog`/`WithClock`)、可观测(`WithObserver`/`WithHandlerObserver`)、
请求体访问(`WithBody`,opt-in 限长、下游不受影响)、Router 指标(`Stats`)。
剩余(非必需):真实 guest 示例(TinyGo/`//go:wasmexport` 编译验证)。

用途:自定义中间件/过滤器、限流/鉴权/改写策略、WAF 规则、可编程 webhook。

## Tier 2 —— 用 wasm 沙箱执行 agent 工具 / skills 脚本 · 已落地(`contrib/wasmagent`)

接 `contrib/llm/agent` 已有工作:`skills.EnableExec` 目前跑**本地脚本**(默认关,因等于信任任意本地命令)。
改为在 wazero WASI 沙箱内运行 → 从"信任本地脚本"变成"能力受限、无法逃逸"的执行,补上 agent 平台的
安全短板(E2B/code-interpreter 思路,但纯 Go 内嵌、无外部进程)。

- ✅ `skills.ScriptExecutor` 类型 + `WithScriptExecutor` 注入口;
- ✅ `contrib/wasmagent.NewWasmExecutor`:适配 skills.ScriptExecutor,wasm 沙箱执行技能脚本;
- ✅ `contrib/wasmagent.ToolFrom`:把预编译 wasm 模块直接包装成 agent.Tool;
- ◻ LLM 生成代码的安全运行环境(code-interpreter 雏形,需嵌入一个解释器 guest)。

## Tier 3 —— 策略即 wasm · 已落地(`contrib/wasmopa`)

OPA 把 Rego 编译成 wasm,在 wazero 沙箱里执行,实现 `pkg/api/authz.Enforcer`:

    opa build -t wasm -e 'authz/allow' policy.rego → policy.wasm
    → wasmopa.New(wasmBytes) → authz.Enforcer

- ✅ OPA wasm ABI 1.2+ 协议(`opa_eval` 一次性求值);
- ✅ `Policy.Eval(ctx, input)`:通用策略求值,可用于 governance / 任意 Rego 决策;
- ✅ `Policy.Authorize(ctx, sub, action, resource)`:实现 `authz.Enforcer`;
- ✅ 实例池(每 slot 独立 Runtime + memory,并发安全);`SetData` 热更新外部数据;
- 纯 Go(wazero),无 CGo、无外部进程,比完整 OPA SDK 轻量得多。

## Tier 4 —— FaaS-lite:wasm 函数即 HTTP Handler · 已落地(`contrib/wasm`)

beauty 作为 wasm 函数宿主:用户上传 .wasm → 注册到路径 → 实例池处理请求。
与 Middleware 共享 alloc/handle ABI,区别在于 guest 输出 **Response**(status + headers + body)
而非 Decision(next/deny)。

- ✅ `Handler(mod)`:把单个 wasm 模块包装成 `http.Handler`;
- ✅ `Router`:FaaS 路由器——`Register`/`Deregister`/`RegisterBytes` 热插拔;
- ✅ 精确匹配 > 最长前缀匹配;并发安全;支持实例池(`WithHandlerPool`)+预热(`WithHandlerWarm`);
- ✅ 超时(`WithHandlerTimeout`)+ 请求体传入(`WithHandlerBody`)+ 可观测(`WithHandlerObserver`);
- ✅ `Router.Stats()`:Functions / Hits / Misses。

用法:

```go
router := wasm.NewRouter(rt)
router.RegisterBytes(ctx, "/greet", greetWasm, wasm.WithHandlerPool(8))
http.Handle("/fn/", http.StripPrefix("/fn", router))
```

## Tier 5 —— Proxy-Wasm ABI 兼容 · 已落地(`contrib/proxywasm`)

基于 wazero 实现 Proxy-Wasm ABI v0.2.1 的 HTTP Filter 子集,使 Higress/Envoy 生态的
WASM 插件(proxy-wasm-go-sdk / proxy-wasm-rust-sdk 编写)无需修改即可作为 Beauty HTTP 中间件运行。

- ✅ 完整 HTTP stream 生命周期:VM start → Plugin configure → per-request stream context;
- ✅ Host functions:proxy_log、header map 6 操作、buffer 读写、send_local_response、
  properties、stream control、时间、定时器;
- ✅ 最小 WASI(fd_write→slog、clock、random、environ/args);
- ✅ 有状态实例池(初始化后可复用)+ 超时中断 + fail-open/closed + 可观测;
- ✅ 输出标准 `func(http.Handler) http.Handler`,无缝接入 webserver/handler;
- ◻ HTTP callout、gRPC、shared data/queue、metrics (stub,按需后续补齐)。

用法:

```go
rt, _ := proxywasm.New(ctx, proxywasm.WithMemoryLimitPages(32))
mod, _ := rt.Compile(ctx, higressPluginWasm)
filter := proxywasm.HTTPFilter(mod,
    proxywasm.WithPluginConfig(configJSON),
    proxywasm.WithPoolSize(16),
)
beauty.WithWebServer(":8080", mux, webserver.WithMiddleware(filter))
```

## 备选(暂不排期)

- js/wasm:把 `gameloop`/`spatial`(AOI)/`presence` 的共享逻辑编到浏览器做客户端预测。
- GOOS=wasip1 部署到 wasm 运行时:wasip1 网络受限,当前仅适合纯计算 handler/worker。

**不做**:把整个 beauty 核心跑成 WASI(wasip1 对网络/多路复用/信号支持不全)。
