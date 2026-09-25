# DAG Executor (pkg/foundation/dag)

> 中文版: [dag.md](dag.md)

`pkg/foundation/dag` is a directed acyclic graph executor with zero external dependencies: it topologically sorts nodes into layers by their dependencies,
**running nodes within the same layer in parallel and layers sequentially**. It is not tied to any database / scheduler / task system;
the work of each node is described by the `Node.Run` closure.

## Quick Start

```go
import "github.com/rushteam/beauty/pkg/foundation/dag"

d := dag.New().Add(
    dag.Node{Name: "build", Run: build},
    dag.Node{Name: "test",   DependsOn: []string{"build"}, Run: test},
    dag.Node{Name: "lint",   DependsOn: []string{"build"}, Run: lint},
    dag.Node{Name: "deploy", DependsOn: []string{"test", "lint"}, Run: deploy},
)
if err := d.Run(ctx); err != nil {
    // build -> [test, lint in parallel] -> deploy
}
```

## Nodes

```go
type Node struct {
    Name      string                          // unique identifier, referenced by other nodes in DependsOn
    DependsOn []string                         // names of prerequisite nodes
    Run       func(ctx context.Context) error  // the work; nil is treated as an empty placeholder node (only aggregates dependencies)
}
```

## Error Strategies

| Strategy | Behavior |
|------|------|
| `dag.FailFast` (default) | When a failure occurs in a layer, stop once the current layer finishes and do not schedule subsequent layers; returns the failing layer's error (multiple errors are combined with `errors.Join`) |
| `dag.ContinueOnError` | Ignore errors and run all layers, finally returning all errors via `errors.Join` |

```go
d := dag.New(dag.WithStrategy(dag.ContinueOnError))
```

## Options

| Option | Description |
|------|------|
| `WithStrategy(s)` | Error handling strategy, default `FailFast` |
| `WithMaxParallel(n)` | Limit the number of nodes executing concurrently **within the same layer** (semaphore), to avoid spawning tens of thousands of goroutines at once for a layer with a large fan-out; `n<=0` (default) means unlimited |

## Validation and Robustness

- `Validate()` (automatically called first by `Run`): empty / duplicate node names, missing dependencies, or cycles → returns an error.
- **Node panic safety**: `Run` executes in goroutines and the framework `recover`s each node; a panic in a single node is converted into a
  `dag node %q panicked: ...` error, so it does not bring down the process, and the other nodes in the same layer complete as usual.
- **Respects ctx cancellation**: `ctx.Err()` is checked between layers; if cancelled, subsequent layers are not scheduled.
- The topological algorithm is queue-based Kahn with complexity `O(V+E)`, preserving input order within each layer (results are deterministic and reproducible).

## Complexity

| | |
|---|---|
| Topological layering | O(V+E) |
| Execution | Sequential across layers; parallel within a layer (can be throttled with `WithMaxParallel`) |
