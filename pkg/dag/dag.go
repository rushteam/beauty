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
	nodes       []Node
	strategy    Strategy
	maxParallel int // 单层内最大并发数，<=0 表示不限制
}

// Option 配置 DAG。
type Option func(*DAG)

// WithStrategy 设置错误处理策略，默认 FailFast。
func WithStrategy(s Strategy) Option {
	return func(d *DAG) { d.strategy = s }
}

// WithMaxParallel 限制同一层内并发执行的节点数（信号量），避免大扇出层
// 一次性起成千上万个 goroutine。n<=0（默认）表示不限制。
func WithMaxParallel(n int) Option {
	return func(d *DAG) { d.maxParallel = n }
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

		layerErrs := runLayer(ctx, layer, d.maxParallel)

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
// maxParallel>0 时用信号量限制同时运行的节点数。
func runLayer(ctx context.Context, layer []Node, maxParallel int) []error {
	if len(layer) == 1 {
		// 单节点无需起 goroutine
		if err := execNode(ctx, layer[0]); err != nil {
			return []error{err}
		}
		return nil
	}

	var sem chan struct{}
	if maxParallel > 0 && maxParallel < len(layer) {
		sem = make(chan struct{}, maxParallel)
	}

	errs := make([]error, len(layer))
	var wg sync.WaitGroup
	for i := range layer {
		if sem != nil {
			sem <- struct{}{} // 达到上限则阻塞，待有空位再调度下一个节点
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if sem != nil {
				defer func() { <-sem }()
			}
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

// execNode 执行单个节点，并捕获其 panic 转为 error，避免一个节点 panic
// 拖垮整个进程（节点 Run 是用户代码，且在 goroutine 中运行）。
func execNode(ctx context.Context, n Node) (err error) {
	if n.Run == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("dag node %q panicked: %v", n.Name, r)
		}
	}()
	if e := n.Run(ctx); e != nil {
		return fmt.Errorf("dag node %q: %w", n.Name, e)
	}
	return nil
}

// topoSort 用 Kahn 算法将节点按依赖分层；同一层的节点之间无依赖、可并行。
// 同时校验：节点名不重复、依赖存在、无环。
// 队列式实现，复杂度 O(V+E)；层内保持输入顺序，结果确定可复现。
func topoSort(nodes []Node) ([][]Node, error) {
	if len(nodes) == 0 {
		return nil, nil
	}

	index := make(map[string]int, len(nodes)) // name -> nodes 下标
	for i := range nodes {
		name := nodes[i].Name
		if name == "" {
			return nil, fmt.Errorf("dag: node at index %d has empty name", i)
		}
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("dag: duplicate node name %q", name)
		}
		index[name] = i
	}

	inDegree := make([]int, len(nodes))
	children := make([][]int, len(nodes)) // dep 下标 -> 依赖它的节点下标
	for i, n := range nodes {
		for _, dep := range n.DependsOn {
			di, ok := index[dep]
			if !ok {
				return nil, fmt.Errorf("dag: node %q depends on unknown node %q", n.Name, dep)
			}
			children[di] = append(children[di], i)
			inDegree[i]++
		}
	}

	// 初始层：入度为 0 的节点，按输入顺序
	var current []int
	for i := range nodes {
		if inDegree[i] == 0 {
			current = append(current, i)
		}
	}

	var layers [][]Node
	visited := 0
	for len(current) > 0 {
		layer := make([]Node, len(current))
		var next []int
		for k, idx := range current {
			layer[k] = nodes[idx]
			for _, ch := range children[idx] {
				inDegree[ch]--
				if inDegree[ch] == 0 {
					next = append(next, ch)
				}
			}
		}
		layers = append(layers, layer)
		visited += len(current)
		current = next
	}

	if visited < len(nodes) {
		return nil, fmt.Errorf("dag: cycle detected")
	}

	return layers, nil
}
