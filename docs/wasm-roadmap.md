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
- `pkg/handler.WithMiddleware` 通用口,声明式绑定 wasm(核心零 contrib 依赖)。

剩余打磨(非收口必需):实例池、真实 guest 示例(TinyGo/`//go:wasmexport`)、请求 body 访问、内置 host func、可观测。

用途:自定义中间件/过滤器、限流/鉴权/改写策略、WAF 规则、可编程 webhook。

## Tier 2 —— 用 wasm 沙箱执行 agent 工具 / skills 脚本

接 `contrib/llm/agent` 已有工作:`skills.EnableExec` 目前跑**本地脚本**(默认关,因等于信任任意本地命令)。
改为在 wazero WASI 沙箱内运行 → 从"信任本地脚本"变成"能力受限、无法逃逸"的执行,补上 agent 平台的
安全短板(E2B/code-interpreter 思路,但纯 Go 内嵌、无外部进程)。

- 给 `agent.Tool` 增加 wasm 执行器,或 skills 增 `WithWasmSandbox`;
- 顺带为"LLM 生成的代码"提供安全运行环境(code-interpreter 雏形);
- 依赖 Tier 1 的运行时。

## 备选(暂不排期)

- 策略即 wasm:OPA 把 Rego 编译成 wasm,接 `pkg/authz` / `governance`。
- FaaS-lite:beauty 作为 wasm 函数宿主(路由 → 用户上传的 wasm 函数,实例池)。
- Proxy-Wasm ABI 兼容:复用现成 Envoy Wasm 过滤器(工程量大)。
- js/wasm:把 `gameloop`/`spatial`(AOI)/`presence` 的共享逻辑编到浏览器做客户端预测。
- GOOS=wasip1 部署到 wasm 运行时:wasip1 网络受限,当前仅适合纯计算 handler/worker。

**不做**:把整个 beauty 核心跑成 WASI(wasip1 对网络/多路复用/信号支持不全)。
