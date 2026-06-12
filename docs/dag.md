# DAG 执行器（pkg/dag）

`pkg/dag` 是一个零外部依赖的有向无环图执行器：按依赖关系将节点拓扑分层，
**同一层内的节点并行执行，层与层之间串行**。不绑定任何数据库 / 调度器 / 任务体系，
每个节点的工作由 `Node.Run` 闭包描述。

## 快速开始

```go
import "github.com/rushteam/beauty/pkg/dag"

d := dag.New().Add(
    dag.Node{Name: "build", Run: build},
    dag.Node{Name: "test",   DependsOn: []string{"build"}, Run: test},
    dag.Node{Name: "lint",   DependsOn: []string{"build"}, Run: lint},
    dag.Node{Name: "deploy", DependsOn: []string{"test", "lint"}, Run: deploy},
)
if err := d.Run(ctx); err != nil {
    // build -> [test, lint 并行] -> deploy
}
```

## 节点

```go
type Node struct {
    Name      string                          // 唯一标识，被其它节点在 DependsOn 中引用
    DependsOn []string                         // 前置依赖节点名
    Run       func(ctx context.Context) error  // 工作；nil 视为空占位节点（仅聚合依赖）
}
```

## 错误策略

| 策略 | 行为 |
|------|------|
| `dag.FailFast`（默认） | 某层出现失败，等本层执行完即停止，不再调度后续层；返回失败层错误（多个用 `errors.Join` 合并） |
| `dag.ContinueOnError` | 忽略错误执行完所有层，最终 `errors.Join` 返回全部错误 |

```go
d := dag.New(dag.WithStrategy(dag.ContinueOnError))
```

## 选项

| 选项 | 说明 |
|------|------|
| `WithStrategy(s)` | 错误处理策略，默认 `FailFast` |
| `WithMaxParallel(n)` | 限制**同一层内**并发执行的节点数（信号量），避免大扇出层一次性起上万 goroutine；`n<=0`（默认）不限制 |

## 校验与健壮性

- `Validate()`（`Run` 会自动先校验）：节点名为空 / 重复、依赖不存在、存在环 → 返回错误。
- **节点 panic 安全**：`Run` 在 goroutine 中执行，框架对每个节点 `recover`，单节点 panic 转为
  `dag node %q panicked: ...` 错误，不会拖垮进程，同层其它节点照常完成。
- **遵循 ctx 取消**：层间检查 `ctx.Err()`，已取消则停止调度后续层。
- 拓扑算法为队列式 Kahn，复杂度 `O(V+E)`，层内保持输入顺序（结果确定可复现）。

## 复杂度

| | |
|---|---|
| 拓扑分层 | O(V+E) |
| 执行 | 层间串行；层内并行（可用 `WithMaxParallel` 限流） |
