// Package dag 提供一个轻量的有向无环图（DAG）执行器：
// 按依赖关系将节点拓扑分层，同一层内的节点并行执行，层间串行。
// 不绑定任何数据库 / 调度器 / 任务体系——每个节点的工作由 Node.Run 闭包描述。
package dag

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Strategy 控制某个节点返回错误时的行为。
type Strategy int

const (
	// FailFast（默认）：当前层一旦有节点失败，等本层执行完后即停止，不再调度后续层。
	FailFast Strategy = iota
	// ContinueOnError：忽略错误继续执行所有层，最终汇总所有错误返回。
	ContinueOnError
)

// Node 是 DAG 中的一个工作单元及其依赖。
type Node struct {
	// Name 节点唯一标识，用于被其它节点在 DependsOn 中引用。
	Name string
	// DependsOn 前置依赖节点名；这些节点全部成功（或在 ContinueOnError 下执行完）后本节点才会运行。
	DependsOn []string
	// Run 节点要执行的工作。为 nil 时视为空节点（仅占位/聚合依赖）。
	Run func(ctx context.Context) error
}

// DAG 是一组按依赖关系执行的节点。零值不可用，请用 New 构造。
type DAG struct {
	nodes    []Node
	strategy Strategy
}

// Option 配置 DAG。
type Option func(*DAG)

// WithStrategy 设置错误处理策略，默认 FailFast。
func WithStrategy(s Strategy) Option {
	return func(d *DAG) { d.strategy = s }
}

// New 创建一个 DAG。
func New(opts ...Option) *DAG {
	d := &DAG{strategy: FailFast}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Add 追加一个或多个节点，返回自身以便链式调用。
func (d *DAG) Add(nodes ...Node) *DAG {
	d.nodes = append(d.nodes, nodes...)
	return d
}

// Validate 校验图的合法性：节点名不重复、依赖均存在、无循环依赖。
func (d *DAG) Validate() error {
	_, err := topoSort(d.nodes)
	return err
}

// Run 校验并执行整个 DAG。层间串行，层内并行，遵循 ctx 取消。
//   - FailFast：返回首个失败层的错误（多个失败用 errors.Join 合并）。
//   - ContinueOnError：执行完所有层，返回所有错误的 errors.Join（无错误则 nil）。
func (d *DAG) Run(ctx context.Context) error {
	layers, err := topoSort(d.nodes)
	if err != nil {
		return err
	}

	var collected []error

	for _, layer := range layers {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(collected, fmt.Errorf("dag canceled: %w", err))...)
		}

		layerErrs := runLayer(ctx, layer)

		if len(layerErrs) > 0 {
			if d.strategy == FailFast {
				return errors.Join(append(collected, layerErrs...)...)
			}
			collected = append(collected, layerErrs...)
		}
	}

	return errors.Join(collected...)
}

// runLayer 并行执行一层节点，返回该层出现的所有错误（已包裹节点名）。
func runLayer(ctx context.Context, layer []Node) []error {
	if len(layer) == 1 {
		// 单节点无需起 goroutine
		if err := execNode(ctx, layer[0]); err != nil {
			return []error{err}
		}
		return nil
	}

	errs := make([]error, len(layer))
	var wg sync.WaitGroup
	for i := range layer {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = execNode(ctx, layer[idx])
		}(i)
	}
	wg.Wait()

	var out []error
	for _, e := range errs {
		if e != nil {
			out = append(out, e)
		}
	}
	return out
}

func execNode(ctx context.Context, n Node) error {
	if n.Run == nil {
		return nil
	}
	if err := n.Run(ctx); err != nil {
		return fmt.Errorf("dag node %q: %w", n.Name, err)
	}
	return nil
}

// topoSort 用 Kahn 算法将节点按依赖分层；同一层的节点之间无依赖、可并行。
// 同时校验：节点名不重复、依赖存在、无环。
func topoSort(nodes []Node) ([][]Node, error) {
	if len(nodes) == 0 {
		return nil, nil
	}

	index := make(map[string]int, len(nodes)) // name -> nodes 下标
	inDegree := make(map[string]int, len(nodes))
	children := make(map[string][]string, len(nodes)) // dep -> 依赖它的节点

	for i := range nodes {
		name := nodes[i].Name
		if name == "" {
			return nil, fmt.Errorf("dag: node at index %d has empty name", i)
		}
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("dag: duplicate node name %q", name)
		}
		index[name] = i
		inDegree[name] = 0
	}

	for _, n := range nodes {
		for _, dep := range n.DependsOn {
			if _, ok := index[dep]; !ok {
				return nil, fmt.Errorf("dag: node %q depends on unknown node %q", n.Name, dep)
			}
			children[dep] = append(children[dep], n.Name)
			inDegree[n.Name]++
		}
	}

	var layers [][]Node
	visited := 0

	for visited < len(nodes) {
		var layer []Node
		for name, deg := range inDegree {
			if deg == 0 {
				layer = append(layer, nodes[index[name]])
			}
		}
		if len(layer) == 0 {
			return nil, fmt.Errorf("dag: cycle detected")
		}
		for _, n := range layer {
			delete(inDegree, n.Name)
			for _, child := range children[n.Name] {
				inDegree[child]--
			}
		}
		layers = append(layers, layer)
		visited += len(layer)
	}

	return layers, nil
}
