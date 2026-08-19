# contrib/wasmopa —— OPA Rego→wasm 授权(独立模块)

用 OPA 编译出的 wasm 策略模块实现 beauty `pkg/api/authz.Enforcer`:Rego 策略经
`opa build -t wasm` 生成 `policy.wasm`,在 wazero 沙箱内求值——纯 Go、无 CGo、无外部 OPA 进程。

```bash
go get github.com/rushteam/beauty/contrib/wasmopa@latest
```

## 编译策略

```bash
# policy.rego 中定义 package authz; default allow = false; allow { ... }
opa build -t wasm -e 'authz/allow' policy.rego
# 从 bundle 取出 policy.wasm
```

## 用法

```go
import (
    "github.com/rushteam/beauty/pkg/api/authz"
    "github.com/rushteam/beauty/contrib/wasmopa"
)

wasmBytes, _ := os.ReadFile("policy.wasm")
policy, _ := wasmopa.New(wasmBytes,
    wasmopa.WithTimeout(5*time.Second),
    wasmopa.WithPool(4),
    wasmopa.WithData(dataJSON), // 可选外部 data document
)
defer policy.Close()

var enforcer authz.Enforcer = policy
mux.Handle("/api/", authz.HTTP(enforcer, mapper)(handler))
```

`Authorize` 自动构造 input `{"subject":...,"action":...,"resource":...}`,判定
`result[0].result.allow == true`(或入口直接返回 bool)。`Eval` 提供通用 JSON 求值,
可用于熔断/路由等 governance 场景。

## 边界

Rego 策略内容、subject 映射、data 热更频率都是 policy;本包负责 OPA wasm ABI 1.2+
(`opa_eval`) 与实例池化。依赖 beauty core(`pkg/api/authz`)。内置最小 `env` wasm 模块满足
OPA builtin stub 需求。
