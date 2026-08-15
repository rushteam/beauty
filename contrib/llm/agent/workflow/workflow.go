// Package workflow 提供声明式图编排引擎,用于构建复杂 agent 工作流。
//
// 与 Chain/Team/Parallel 代码级组合互补:图引擎适合条件分支、循环审批、动态路由等
// 用代码组合难以表达的复杂编排。简单场景仍推荐 Chain/Team/Parallel。
//
// 核心概念:
//   - Node: 图中的一个执行节点(agent 调用、工具调用、条件判断、HITL 审批等)
//   - Edge: 节点间的连接(直连、条件、扇出/扇入)
//   - Workflow: 不可变图定义(由 Builder 构建)
//   - Engine: 执行器,驱动图的运行并管理状态
//
// 设计原则:图定义与执行分离;节点行为通过 NodeFunc 注入,引擎不关心具体逻辑。
package workflow

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// NodeID 是节点的唯一标识。
type NodeID string

// Reserved node IDs.
const (
	StartNode NodeID = "__start__"
	EndNode   NodeID = "__end__"
)

// NodeFunc 定义节点的执行逻辑。接收当前状态,返回输出和下一步路由键。
// routeKey 用于条件边的匹配;空串表示走默认边。
type NodeFunc func(ctx context.Context, state *State) (routeKey string, err error)

// Node 是图中的一个节点。
type Node struct {
	ID   NodeID
	Func NodeFunc
}

// EdgeType 标识边的类型。
type EdgeType int

const (
	EdgeDirect      EdgeType = iota // 无条件直连
	EdgeConditional                 // 条件路由(基于 routeKey)
	EdgeFanOut                      // 扇出(并发执行多个目标)
	EdgeFanIn                       // 扇入屏障(等待所有源完成)
)

// Edge 连接两个节点。
type Edge struct {
	From     NodeID
	To       NodeID
	Type     EdgeType
	RouteMap map[string]NodeID // EdgeConditional: routeKey → target
	FanOut   []NodeID          // EdgeFanOut: 并发目标
}

// Checkpoint 是工作流执行的检查点,可序列化以实现暂停/恢复。
type Checkpoint struct {
	WorkflowID  string
	CurrentNode NodeID
	State       *State
	Completed   map[NodeID]bool
	StepCount   int
}

// State 是工作流执行过程中的可变状态容器。
type State struct {
	mu     sync.RWMutex
	values map[string]any
	msgs   []llm.Message
	output *llm.Response
}

// NewState 创建空状态。
func NewState() *State {
	return &State{values: make(map[string]any)}
}

// Set 设置键值对。
func (s *State) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
}

// Get 获取值。
func (s *State) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[key]
	return v, ok
}

// GetTyped 类型安全的值获取。
func GetTyped[T any](s *State, key string) (T, bool) {
	v, ok := s.Get(key)
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// Messages 返回当前消息列表。
func (s *State) Messages() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]llm.Message(nil), s.msgs...)
}

// AppendMessage 追加消息。
func (s *State) AppendMessage(m llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
}

// SetOutput 设置最终输出。
func (s *State) SetOutput(r *llm.Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output = r
}

// Output 返回最终输出。
func (s *State) Output() *llm.Response {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.output
}

// Workflow 是不可变的图定义。
type Workflow struct {
	ID    string
	nodes map[NodeID]*Node
	edges map[NodeID][]Edge
}

// Builder 用于流畅地构建 Workflow。
type Builder struct {
	id     string
	nodes  map[NodeID]*Node
	edges  map[NodeID][]Edge
	errors []error
}

// NewBuilder 创建工作流构建器。
func NewBuilder(id string) *Builder {
	return &Builder{
		id:    id,
		nodes: make(map[NodeID]*Node),
		edges: make(map[NodeID][]Edge),
	}
}

// AddNode 添加节点。
func (b *Builder) AddNode(id NodeID, fn NodeFunc) *Builder {
	if id == StartNode || id == EndNode {
		b.errors = append(b.errors, fmt.Errorf("workflow: cannot add reserved node %q", id))
		return b
	}
	if _, exists := b.nodes[id]; exists {
		b.errors = append(b.errors, fmt.Errorf("workflow: duplicate node %q", id))
		return b
	}
	b.nodes[id] = &Node{ID: id, Func: fn}
	return b
}

// AddEdge 添加无条件直连边。
func (b *Builder) AddEdge(from, to NodeID) *Builder {
	b.edges[from] = append(b.edges[from], Edge{From: from, To: to, Type: EdgeDirect})
	return b
}

// AddConditionalEdge 添加条件边:from 节点的 routeKey 匹配 routeMap 中的 key。
// defaultTarget 是无匹配时的默认目标(空则报错)。
func (b *Builder) AddConditionalEdge(from NodeID, routeMap map[string]NodeID, defaultTarget NodeID) *Builder {
	if defaultTarget != "" {
		routeMap[""] = defaultTarget
	}
	b.edges[from] = append(b.edges[from], Edge{
		From:     from,
		To:       defaultTarget,
		Type:     EdgeConditional,
		RouteMap: routeMap,
	})
	return b
}

// AddFanOut 添加扇出边:from 完成后并发执行所有 targets。
func (b *Builder) AddFanOut(from NodeID, targets ...NodeID) *Builder {
	b.edges[from] = append(b.edges[from], Edge{
		From:   from,
		Type:   EdgeFanOut,
		FanOut: targets,
	})
	return b
}

// AddFanIn 添加扇入屏障边:等待所有 sources 完成后执行 target。
func (b *Builder) AddFanIn(sources []NodeID, target NodeID) *Builder {
	for _, src := range sources {
		b.edges[src] = append(b.edges[src], Edge{
			From: src,
			To:   target,
			Type: EdgeFanIn,
		})
	}
	return b
}

// SetEntryPoint 设置起始节点(从 __start__ 到 target)。
func (b *Builder) SetEntryPoint(target NodeID) *Builder {
	return b.AddEdge(StartNode, target)
}

// SetFinishPoint 设置结束节点(从 source 到 __end__)。
func (b *Builder) SetFinishPoint(source NodeID) *Builder {
	return b.AddEdge(source, EndNode)
}

// Build 构建不可变的 Workflow。有错误时返回 nil + error。
func (b *Builder) Build() (*Workflow, error) {
	if len(b.errors) > 0 {
		return nil, fmt.Errorf("workflow: build errors: %v", b.errors)
	}
	if _, ok := b.edges[StartNode]; !ok {
		return nil, fmt.Errorf("workflow: no entry point (use SetEntryPoint)")
	}
	return &Workflow{
		ID:    b.id,
		nodes: b.nodes,
		edges: b.edges,
	}, nil
}

// Engine 驱动 Workflow 的执行。
type Engine struct {
	workflow     *Workflow
	maxSteps     int
	onStep       func(step int, nodeID NodeID)
	checkpointFn func(ctx context.Context, cp *Checkpoint) error
}

// EngineOption 配置 Engine。
type EngineOption func(*Engine)

// WithMaxSteps 设置最大执行步数(防止无限循环)。
func WithMaxSteps(n int) EngineOption {
	return func(e *Engine) { e.maxSteps = n }
}

// WithOnStep 设置步骤回调。
func WithOnStep(fn func(step int, nodeID NodeID)) EngineOption {
	return func(e *Engine) { e.onStep = fn }
}

// WithCheckpointFunc 设置检查点回调(每步完成后调用)。
func WithCheckpointFunc(fn func(ctx context.Context, cp *Checkpoint) error) EngineOption {
	return func(e *Engine) { e.checkpointFn = fn }
}

// NewEngine 创建执行引擎。
func NewEngine(wf *Workflow, opts ...EngineOption) *Engine {
	e := &Engine{workflow: wf, maxSteps: 100}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Run 同步执行工作流,返回最终输出。
func (e *Engine) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	var result *llm.Response
	for ev, err := range e.RunIter(ctx, req) {
		if err != nil {
			return nil, err
		}
		if ev.Type == agent.EventFinal {
			result = ev.Response
		}
	}
	return result, nil
}

// RunIter 迭代器模式执行工作流,逐步产出事件。
func (e *Engine) RunIter(ctx context.Context, req llm.Request) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {
		state := NewState()
		for _, m := range req.Messages {
			state.AppendMessage(m)
		}
		state.Set("system", req.System)
		state.Set("model", req.Model)

		completed := make(map[NodeID]bool)
		step := 0

		current := e.resolveStart()
		if current == "" {
			yield(agent.Event{}, fmt.Errorf("workflow: cannot resolve entry point"))
			return
		}

		for current != EndNode && step < e.maxSteps {
			if err := ctx.Err(); err != nil {
				yield(agent.Event{}, err)
				return
			}
			step++

			node, ok := e.workflow.nodes[current]
			if !ok {
				yield(agent.Event{}, fmt.Errorf("workflow: unknown node %q", current))
				return
			}

			if e.onStep != nil {
				e.onStep(step, current)
			}

			routeKey, err := node.Func(ctx, state)
			if err != nil {
				yield(agent.Event{}, fmt.Errorf("workflow: node %q: %w", current, err))
				return
			}
			completed[current] = true

			if !yield(agent.Event{
				Type:      agent.EventStep,
				Step:      step,
				AgentName: string(current),
				Result:    routeKey,
			}, nil) {
				return
			}

			if e.checkpointFn != nil {
				cp := &Checkpoint{
					WorkflowID:  e.workflow.ID,
					CurrentNode: current,
					State:       state,
					Completed:   completed,
					StepCount:   step,
				}
				if err := e.checkpointFn(ctx, cp); err != nil {
					yield(agent.Event{}, fmt.Errorf("workflow: checkpoint: %w", err))
					return
				}
			}

			next, err := e.resolveNext(current, routeKey, state)
			if err != nil {
				yield(agent.Event{}, err)
				return
			}
			current = next
		}

		if step >= e.maxSteps {
			yield(agent.Event{}, fmt.Errorf("workflow: exceeded max steps (%d)", e.maxSteps))
			return
		}

		output := state.Output()
		if output == nil {
			output = &llm.Response{Content: "workflow completed"}
		}
		yield(agent.Event{Type: agent.EventFinal, Response: output}, nil)
	}
}

func (e *Engine) resolveStart() NodeID {
	edges, ok := e.workflow.edges[StartNode]
	if !ok || len(edges) == 0 {
		return ""
	}
	return edges[0].To
}

func (e *Engine) resolveNext(current NodeID, routeKey string, state *State) (NodeID, error) {
	edges, ok := e.workflow.edges[current]
	if !ok || len(edges) == 0 {
		return EndNode, nil
	}
	edge := edges[0]
	switch edge.Type {
	case EdgeDirect:
		return edge.To, nil
	case EdgeConditional:
		if target, ok := edge.RouteMap[routeKey]; ok {
			return target, nil
		}
		if target, ok := edge.RouteMap[""]; ok {
			return target, nil
		}
		return "", fmt.Errorf("workflow: node %q returned route key %q with no matching edge", current, routeKey)
	case EdgeFanOut:
		return e.executeFanOut(state, edge)
	default:
		return edge.To, nil
	}
}

func (e *Engine) executeFanOut(state *State, edge Edge) (NodeID, error) {
	var wg sync.WaitGroup
	errs := make([]error, len(edge.FanOut))
	for i, target := range edge.FanOut {
		wg.Add(1)
		go func(i int, target NodeID) {
			defer wg.Done()
			node, ok := e.workflow.nodes[target]
			if !ok {
				errs[i] = fmt.Errorf("workflow: fan-out unknown node %q", target)
				return
			}
			_, errs[i] = node.Func(context.Background(), state)
		}(i, target)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return "", err
		}
	}
	// 扇出后找扇入目标
	for _, target := range edge.FanOut {
		if fanInEdges, ok := e.workflow.edges[target]; ok {
			for _, fe := range fanInEdges {
				if fe.Type == EdgeFanIn {
					return fe.To, nil
				}
			}
		}
	}
	return EndNode, nil
}

// ---- 便捷节点工厂 ----

// AgentNode 把一个 agent.Agent 包装成 NodeFunc。
func AgentNode(a agent.Agent, model, system string) NodeFunc {
	return func(ctx context.Context, state *State) (string, error) {
		msgs := state.Messages()
		m, _ := GetTyped[string](state, "model")
		if model != "" {
			m = model
		}
		sys, _ := GetTyped[string](state, "system")
		if system != "" {
			sys = system
		}
		out := agent.CollectOutcome(a.Run(ctx, llm.Request{
			Model:    m,
			System:   sys,
			Messages: msgs,
		}))
		switch out.Status {
		case agent.StatusDone:
			if out.Response != nil {
				state.AppendMessage(llm.Message{
					Role:    llm.Assistant,
					Content: out.Response.Content,
					Source:  llm.SourceModel,
				})
				state.SetOutput(out.Response)
			}
			return "", nil
		case agent.StatusPaused:
			return "paused", nil
		default:
			return "", out.Err
		}
	}
}

// ConditionNode 创建一个纯逻辑条件判断节点。
func ConditionNode(decide func(ctx context.Context, state *State) (string, error)) NodeFunc {
	return decide
}

// LLMNode 创建一个直接调用 LLM 的节点(不经过 agent 循环)。
func LLMNode(client llm.Client, model, system string) NodeFunc {
	return func(ctx context.Context, state *State) (string, error) {
		msgs := state.Messages()
		m, _ := GetTyped[string](state, "model")
		if model != "" {
			m = model
		}
		sys, _ := GetTyped[string](state, "system")
		if system != "" {
			sys = system
		}
		resp, err := client.Generate(ctx, llm.Request{
			Model:    m,
			System:   sys,
			Messages: msgs,
		})
		if err != nil {
			return "", err
		}
		state.AppendMessage(llm.Message{
			Role:    llm.Assistant,
			Content: resp.Content,
			Source:  llm.SourceModel,
		})
		state.SetOutput(resp)
		return "", nil
	}
}

// TransformNode 创建一个消息转换节点(纯数据处理,不调模型)。
func TransformNode(transform func(ctx context.Context, state *State) error) NodeFunc {
	return func(ctx context.Context, state *State) (string, error) {
		return "", transform(ctx, state)
	}
}
