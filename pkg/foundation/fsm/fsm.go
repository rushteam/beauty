// Package fsm 提供泛型有限状态机原语:声明式定义"状态 + 事件 → 目标状态"的
// 合法转移表,运行时 Fire(event) 只允许沿合法转移推进,非法转移报错而非静默改状态。
//
// 解决的问题:对局(等待→进行→结算)、房间生命周期、订单、匹配状态,业务里常
// 用裸 if/switch 手写状态流转,容易出现非法跳转(如已结算又被改回进行中)且难审计。
// 本包把合法转移集中声明,把"能不能转"交给状态机校验,并在转移前后挂钩子。
//
// 泛型 S(状态)、E(事件)须为 comparable(用作 map 键),通常是自定义的
// 整型或字符串枚举。钩子执行顺序:OnLeave(from) → OnTransition(from,to,event)
// → 状态切换 → OnEnter(to)。任一钩子返回 error 则中止转移,状态不变。
//
// 并发安全:Fire 全程持锁,钩子在锁内执行(须轻量、不可回调 Fire 以免死锁)。
// 零值不可用,用 New / NewBuilder 构造。
//
// 增强特性:
//   - Guard 守卫条件:AllowIf(from, event, to, guard) 运行时条件转移;
//   - HSM 层次状态机:Parent(child, parent) 事件在子状态无匹配时向父冒泡;
//   - 状态超时:Timeout(state, dur, event) 在某状态停留超时后自动 Fire;
//   - 可视化导出:ExportDOT / ExportMermaid 生成状态图。
package fsm

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// transition 一条转移的 (from, event) 复合键。
type transition[S, E comparable] struct {
	from  S
	event E
}

// Guard 转移守卫条件。返回 true 允许转移,false 跳过该转移(尝试下一条或冒泡到父状态)。
type Guard[S, E comparable] func(from S, event E) bool

// guardedTarget 一条带可选守卫的转移目标。
type guardedTarget[S, E comparable] struct {
	to    S
	guard Guard[S, E] // nil = 无条件通过
}

// timeoutEntry 状态超时配置。
type timeoutEntry[S, E comparable] struct {
	duration time.Duration
	event    E
}

// Callbacks 转移生命周期钩子。任一返回 error 会中止转移,状态保持不变。
type Callbacks[S, E comparable] struct {
	// OnLeave 离开 from 状态前调用。
	OnLeave func(from S, event E) error
	// OnTransition 状态即将从 from 切到 to 时调用(状态尚未变更)。
	OnTransition func(from, to S, event E) error
	// OnEnter 进入 to 状态后调用(状态已变更)。此钩子返回的 error 只上报,不回滚。
	OnEnter func(to S, event E) error
}

// FSM 泛型有限状态机。零值不可用,用 NewBuilder 构造。并发安全。
type FSM[S, E comparable] struct {
	mu       sync.RWMutex
	state    S
	transit  map[transition[S, E]][]guardedTarget[S, E]
	parents  map[S]S                  // HSM:子状态 → 父状态
	timeouts map[S]timeoutEntry[S, E] // 状态超时配置
	cb       Callbacks[S, E]

	// 超时管理
	timerMu sync.Mutex
	timer   *time.Timer
	closed  bool
}

// ErrInvalidTransition 当前状态下该事件无合法转移。
type ErrInvalidTransition[S, E comparable] struct {
	From  S
	Event E
}

func (e ErrInvalidTransition[S, E]) Error() string {
	return fmt.Sprintf("fsm: no transition from state %v on event %v", e.From, e.Event)
}

// ErrGuardRejected 存在转移但所有 Guard 条件均不满足。
type ErrGuardRejected[S, E comparable] struct {
	From  S
	Event E
}

func (e ErrGuardRejected[S, E]) Error() string {
	return fmt.Sprintf("fsm: all guards rejected transition from state %v on event %v", e.From, e.Event)
}

// newFSM 内部构造器。
func newFSM[S, E comparable](
	initial S,
	transitions map[transition[S, E]][]guardedTarget[S, E],
	parents map[S]S,
	timeouts map[S]timeoutEntry[S, E],
	cb Callbacks[S, E],
) *FSM[S, E] {
	// 深拷贝
	t := make(map[transition[S, E]][]guardedTarget[S, E], len(transitions))
	for k, v := range transitions {
		cp := make([]guardedTarget[S, E], len(v))
		copy(cp, v)
		t[k] = cp
	}
	p := make(map[S]S, len(parents))
	for k, v := range parents {
		p[k] = v
	}
	to := make(map[S]timeoutEntry[S, E], len(timeouts))
	for k, v := range timeouts {
		to[k] = v
	}
	f := &FSM[S, E]{
		state:    initial,
		transit:  t,
		parents:  p,
		timeouts: to,
		cb:       cb,
	}
	// 初始状态可能有超时
	f.resetTimeout()
	return f
}

// Current 返回当前状态。
func (f *FSM[S, E]) Current() S {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.state
}

// Is 判断当前是否处于 s 状态。
func (f *FSM[S, E]) Is(s S) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.state == s
}

// IsIn 判断当前是否处于 s 状态或其任意子状态(HSM)。
func (f *FSM[S, E]) IsIn(s S) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.isIn(f.state, s)
}

func (f *FSM[S, E]) isIn(current, target S) bool {
	if current == target {
		return true
	}
	parent, ok := f.parents[current]
	if !ok {
		return false
	}
	return f.isIn(parent, target)
}

// Can 判断当前状态下 event 是否有合法转移(不执行,不检查 Guard)。
func (f *FSM[S, E]) Can(event E) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.resolveTargets(f.state, event) != nil
}

// Fire 触发一个事件,沿合法转移推进状态。
//
// 无合法转移 → 返回 ErrInvalidTransition,状态不变。
// 有转移但所有 Guard 不通过 → 返回 ErrGuardRejected,状态不变。
// 钩子顺序:OnLeave → OnTransition →(切换状态)→ OnEnter。
// OnLeave/OnTransition 返回 error → 中止,状态不变,返回该 error;
// OnEnter 返回 error → 状态已切换,error 一并返回(供调用方记录,不回滚)。
// HSM:当前状态无匹配时事件自动冒泡到父状态查找转移。
func (f *FSM[S, E]) Fire(event E) (S, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	from := f.state
	targets := f.resolveTargets(from, event)
	if targets == nil {
		return from, ErrInvalidTransition[S, E]{From: from, Event: event}
	}

	// 按顺序检查 Guard,第一个通过的生效
	var to S
	found := false
	for _, gt := range targets {
		if gt.guard == nil || gt.guard(from, event) {
			to = gt.to
			found = true
			break
		}
	}
	if !found {
		return from, ErrGuardRejected[S, E]{From: from, Event: event}
	}

	if f.cb.OnLeave != nil {
		if err := f.cb.OnLeave(from, event); err != nil {
			return from, fmt.Errorf("fsm: OnLeave aborted transition %v->%v: %w", from, to, err)
		}
	}
	if f.cb.OnTransition != nil {
		if err := f.cb.OnTransition(from, to, event); err != nil {
			return from, fmt.Errorf("fsm: OnTransition aborted %v->%v: %w", from, to, err)
		}
	}
	f.state = to
	f.resetTimeout()
	if f.cb.OnEnter != nil {
		if err := f.cb.OnEnter(to, event); err != nil {
			return to, fmt.Errorf("fsm: OnEnter after %v->%v: %w", from, to, err)
		}
	}
	return to, nil
}

// resolveTargets 查找 (state, event) 的转移目标列表,支持 HSM 冒泡。
func (f *FSM[S, E]) resolveTargets(state S, event E) []guardedTarget[S, E] {
	key := transition[S, E]{from: state, event: event}
	if targets, ok := f.transit[key]; ok {
		return targets
	}
	// HSM 冒泡:向父状态查找
	if parent, ok := f.parents[state]; ok {
		return f.resolveTargets(parent, event)
	}
	return nil
}

// AvailableEvents 返回当前状态下所有可触发的事件(含从父状态继承的,顺序不保证)。
func (f *FSM[S, E]) AvailableEvents() []E {
	f.mu.RLock()
	defer f.mu.RUnlock()
	seen := make(map[E]bool)
	f.collectEvents(f.state, seen)
	out := make([]E, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	return out
}

func (f *FSM[S, E]) collectEvents(state S, seen map[E]bool) {
	for k := range f.transit {
		if k.from == state {
			seen[k.event] = true
		}
	}
	if parent, ok := f.parents[state]; ok {
		f.collectEvents(parent, seen)
	}
}

// Close 停止超时定时器,释放资源。调用后 Fire 仍可用,但不会再启动新的超时。
func (f *FSM[S, E]) Close() {
	f.timerMu.Lock()
	defer f.timerMu.Unlock()
	f.closed = true
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
}

// resetTimeout 重置超时定时器(在状态切换后调用,持有 f.mu)。
func (f *FSM[S, E]) resetTimeout() {
	f.timerMu.Lock()
	defer f.timerMu.Unlock()
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	if f.closed {
		return
	}
	tc, ok := f.timeouts[f.state]
	if !ok || tc.duration <= 0 {
		return
	}
	event := tc.event
	f.timer = time.AfterFunc(tc.duration, func() {
		f.Fire(event) // 忽略返回值:状态可能已变(超时过期)
	})
}

// ===== 可视化导出 =====

// ExportDOT 导出 GraphViz DOT 格式的状态图。
//
//	dot -Tpng -o fsm.png <<< "$(m.ExportDOT())"
func (f *FSM[S, E]) ExportDOT() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var b strings.Builder
	b.WriteString("digraph FSM {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString(fmt.Sprintf("  node [shape=circle];\n"))
	// 标记当前状态
	b.WriteString(fmt.Sprintf("  \"%v\" [style=filled, fillcolor=lightblue];\n", f.state))
	// 标记超时状态
	for s, tc := range f.timeouts {
		b.WriteString(fmt.Sprintf("  \"%v\" [xlabel=\"⏱%v→%v\"];\n", s, tc.duration, tc.event))
	}
	// 转移边
	for key, targets := range f.transit {
		for i, gt := range targets {
			label := fmt.Sprintf("%v", key.event)
			if gt.guard != nil {
				label += fmt.Sprintf(" [guard#%d]", i+1)
			}
			b.WriteString(fmt.Sprintf("  \"%v\" -> \"%v\" [label=\"%s\"];\n", key.from, gt.to, label))
		}
	}
	// HSM 父子关系(虚线)
	for child, parent := range f.parents {
		b.WriteString(fmt.Sprintf("  \"%v\" -> \"%v\" [style=dashed, label=\"parent\", color=gray];\n", child, parent))
	}
	b.WriteString("}\n")
	return b.String()
}

// ExportMermaid 导出 Mermaid stateDiagram-v2 格式。
//
//	```mermaid
//	stateDiagram-v2
//	  ...
//	```
func (f *FSM[S, E]) ExportMermaid() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var b strings.Builder
	b.WriteString("stateDiagram-v2\n")
	// HSM 父子嵌套
	children := make(map[S][]S) // parent → children
	for child, parent := range f.parents {
		children[parent] = append(children[parent], child)
	}
	// 渲染嵌套状态(简化:只支持一级嵌套)
	for parent, kids := range children {
		b.WriteString(fmt.Sprintf("  state %v {\n", parent))
		for _, kid := range kids {
			b.WriteString(fmt.Sprintf("    %v\n", kid))
		}
		b.WriteString("  }\n")
	}
	// 转移
	for key, targets := range f.transit {
		for i, gt := range targets {
			label := fmt.Sprintf("%v", key.event)
			if gt.guard != nil {
				label += fmt.Sprintf(" [guard#%d]", i+1)
			}
			b.WriteString(fmt.Sprintf("  %v --> %v : %s\n", key.from, gt.to, label))
		}
	}
	// 超时注释
	for s, tc := range f.timeouts {
		b.WriteString(fmt.Sprintf("  note right of %v : ⏱ %v → %v\n", s, tc.duration, tc.event))
	}
	return b.String()
}

// ===== Builder =====

// Builder 以链式 API 声明转移表与钩子,避免手写 transition 复合键。
type Builder[S, E comparable] struct {
	initial     S
	transitions map[transition[S, E]][]guardedTarget[S, E]
	parents     map[S]S
	timeouts    map[S]timeoutEntry[S, E]
	cb          Callbacks[S, E]
}

// NewBuilder 创建以 initial 为初始状态的 Builder。
func NewBuilder[S, E comparable](initial S) *Builder[S, E] {
	return &Builder[S, E]{
		initial:     initial,
		transitions: make(map[transition[S, E]][]guardedTarget[S, E]),
		parents:     make(map[S]S),
		timeouts:    make(map[S]timeoutEntry[S, E]),
	}
}

// Allow 声明一条无条件转移:from 状态收到 event 时转到 to。
// 重复声明同一 (from, event) 后者覆盖前者(清除该键上的所有 Guard 转移)。
func (b *Builder[S, E]) Allow(from S, event E, to S) *Builder[S, E] {
	key := transition[S, E]{from: from, event: event}
	b.transitions[key] = []guardedTarget[S, E]{{to: to}}
	return b
}

// AllowIf 声明一条带 Guard 条件的转移:from 状态收到 event 且 guard 返回 true 时转到 to。
// 同一 (from, event) 可多次 AllowIf,按声明顺序求值,第一个 guard 通过的生效。
//
//	b.AllowIf(idle, start, running, func(from S, e E) bool { return isReady() }).
//	  AllowIf(idle, start, error,   func(from S, e E) bool { return !isReady() })
func (b *Builder[S, E]) AllowIf(from S, event E, to S, guard Guard[S, E]) *Builder[S, E] {
	key := transition[S, E]{from: from, event: event}
	b.transitions[key] = append(b.transitions[key], guardedTarget[S, E]{to: to, guard: guard})
	return b
}

// Parent 声明 HSM 父子关系:child 是 parent 的子状态。
// 当 child 状态收到事件但无匹配转移时,事件自动冒泡到 parent 查找。
//
//	b.Parent(subIdle, idle).  // subIdle 是 idle 的子状态
//	  Parent(subBusy, idle)   // subBusy 也是 idle 的子状态
func (b *Builder[S, E]) Parent(child, parent S) *Builder[S, E] {
	b.parents[child] = parent
	return b
}

// Timeout 设置状态超时:在 state 停留超过 d 后自动 Fire(event)。
// 每次进入该状态时重置计时器。需在不再使用时调 FSM.Close() 释放定时器。
//
//	b.Timeout(playing, 30*time.Second, timeout) // playing 30s 后自动 fire timeout
func (b *Builder[S, E]) Timeout(state S, d time.Duration, event E) *Builder[S, E] {
	b.timeouts[state] = timeoutEntry[S, E]{duration: d, event: event}
	return b
}

// OnLeave 设置离开状态钩子。
func (b *Builder[S, E]) OnLeave(fn func(from S, event E) error) *Builder[S, E] {
	b.cb.OnLeave = fn
	return b
}

// OnTransition 设置转移钩子(状态切换前)。
func (b *Builder[S, E]) OnTransition(fn func(from, to S, event E) error) *Builder[S, E] {
	b.cb.OnTransition = fn
	return b
}

// OnEnter 设置进入状态钩子(状态切换后)。
func (b *Builder[S, E]) OnEnter(fn func(to S, event E) error) *Builder[S, E] {
	b.cb.OnEnter = fn
	return b
}

// Build 构造 FSM。
func (b *Builder[S, E]) Build() *FSM[S, E] {
	return newFSM(b.initial, b.transitions, b.parents, b.timeouts, b.cb)
}
