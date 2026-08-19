# contrib/wasmagent —— wasm × agent 胶水(独立模块)

把 `contrib/wasm` 的沙箱执行接到 `contrib/llm/agent` 工具循环上:

- **NewWasmExecutor**:适配 `skills.ScriptExecutor`,技能 `scripts/` 目录放 `.wasm` 模块(替代 `EnableExec` 本地进程)
- **ToolFrom**:直接把 wasm 模块包装成 `agent.Tool`(模型调用 → wasm handle → 文本结果)

刻意独立的胶水模块,让 `wasm` 与 `llm/agent` 彼此零耦合。

```bash
go get github.com/rushteam/beauty/contrib/wasmagent@latest
```

## 技能脚本(wasm 沙箱执行)

```go
import (
    "github.com/rushteam/beauty/contrib/wasm"
    "github.com/rushteam/beauty/contrib/wasmagent"
)

rt, _ := wasm.New(ctx, wasm.WithMemoryLimitPages(16))
defer rt.Close(ctx)

exec := wasmagent.NewWasmExecutor(rt,
    wasmagent.WithTimeout(30*time.Second),
    wasmagent.WithPool(4),
)
skills.Load(dir).WithScriptExecutor(exec).EnableExec(30 * time.Second)
```

## wasm 模块即 agent.Tool

```go
mod, _ := rt.Compile(ctx, toolWasm)
tool := wasmagent.ToolFrom(mod, "calc", "做算术", paramsJSON,
    wasmagent.WithTimeout(10*time.Second),
)
runner := agent.NewRunner(provider, agent.WithTools(tool))
```

## guest ABI

与 `contrib/wasm` 中间件同一套:导出 `memory`、`alloc(i32)→i32`、`handle(i32,i32)→i64`
(返回 `(respPtr<<32)|respLen`)。输入 JSON:`{"args":[...],"cwd":"技能目录"}`;输出为**纯文本**
(直接喂回模型)。

## 边界

授予哪些 host 能力、内存上限、出错策略都是 policy;本包负责编译缓存(按 path+mtime)、
实例池与 ABI 调用。依赖 `contrib/wasm` + `contrib/llm`。
