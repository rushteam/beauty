package wasmagent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/rushteam/beauty/contrib/wasm"
	"github.com/rushteam/beauty/contrib/wasmagent"
)

func Example() {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close(ctx)

	// === 路径 A:skills 脚本执行器(技能 scripts/ 里放 .wasm)===
	//
	// exec := wasmagent.NewWasmExecutor(rt, wasmagent.WithTimeout(10*time.Second))
	// skills.Load(...).WithScriptExecutor(exec.Exec).EnableExec(10*time.Second)
	//
	// 模型调用 get_skill_script(execute=true) 时会走 exec.Exec(ctx, path, cwd, args)。

	// === 路径 B:ToolFrom 把预编译模块直接包装成 agent.Tool ===
	wasmBytes := buildGuest("42")
	mod, err := rt.Compile(ctx, wasmBytes)
	if err != nil {
		log.Fatal(err)
	}
	tool := wasmagent.ToolFrom(mod, "calculate", "计算工具",
		json.RawMessage(`{"type":"object","properties":{"expr":{"type":"string"}},"required":["expr"]}`),
		wasmagent.WithTimeout(5*time.Second),
		wasmagent.WithPool(2),
	)

	// 模拟模型调用:
	out, err := tool.Call(ctx, json.RawMessage(`{"expr":"6*7"}`))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)
	// Output: 42
}
