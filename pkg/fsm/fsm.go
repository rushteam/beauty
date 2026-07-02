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
package fsm

import (
	"fmt"
	"maps"
	"sync"
)

// transition 一条转移的 (from, event) 复合键。
type transition[S, E comparable] struct {
	from  S
	event E
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

// FSM 泛型有限状态机。零值不可用,用 New / NewBuilder 构造。并发安全。
type FSM[S, E comparable] struct {
	mu      sync.RWMutex
	state   S
	transit map[transition[S, E]]S
	cb      Callbacks[S, E]
}

// ErrInvalidTransition 当前状态下该事件无合法转移。
type ErrInvalidTransition[S, E comparable] struct {
	From  S
	Event E
}

func (e ErrInvalidTransition[S, E]) Error() string {
	return fmt.Sprintf("fsm: no transition from state %v on event %v", e.From, e.Event)
}

// newFSM 用初始状态、转移表、钩子构造状态机(内部构造器,外部走 NewBuilder)。
func newFSM[S, E comparable](initial S, transitions map[transition[S, E]]S, cb Callbacks[S, E]) *FSM[S, E] {
	m := make(map[transition[S, E]]S, len(transitions))
	maps.Copy(m, transitions)
	return &FSM[S, E]{state: initial, transit: m, cb: cb}
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

// Can 判断当前状态下 event 是否有合法转移(不执行)。
func (f *FSM[S, E]) Can(event E) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.transit[transition[S, E]{from: f.state, event: event}]
	return ok
}

// Fire 触发一个事件,沿合法转移推进状态。
//
// 无合法转移 → 返回 ErrInvalidTransition,状态不变。
// 钩子顺序:OnLeave → OnTransition →(切换状态)→ OnEnter。
// OnLeave/OnTransition 返回 error → 中止,状态不变,返回该 error;
// OnEnter 返回 error → 状态已切换,error 一并返回(供调用方记录,不回滚)。
// 返回切换后的状态(未切换时为原状态)。
func (f *FSM[S, E]) Fire(event E) (S, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	from := f.state
	to, ok := f.transit[transition[S, E]{from: from, event: event}]
	if !ok {
		return from, ErrInvalidTransition[S, E]{From: from, Event: event}
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
	if f.cb.OnEnter != nil {
		if err := f.cb.OnEnter(to, event); err != nil {
			return to, fmt.Errorf("fsm: OnEnter after %v->%v: %w", from, to, err)
		}
	}
	return to, nil
}

// AvailableEvents 返回当前状态下所有可触发的事件(顺序不保证)。
func (f *FSM[S, E]) AvailableEvents() []E {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []E
	for k := range f.transit {
		if k.from == f.state {
			out = append(out, k.event)
		}
	}
	return out
}

// ===== Builder =====

// Builder 以链式 API 声明转移表与钩子,避免手写 transition 复合键。
type Builder[S, E comparable] struct {
	initial     S
	transitions map[transition[S, E]]S
	cb          Callbacks[S, E]
}

// NewBuilder 创建以 initial 为初始状态的 Builder。
func NewBuilder[S, E comparable](initial S) *Builder[S, E] {
	return &Builder[S, E]{initial: initial, transitions: make(map[transition[S, E]]S)}
}

// Allow 声明一条转移:from 状态收到 event 时转到 to。重复声明后者覆盖前者。
func (b *Builder[S, E]) Allow(from S, event E, to S) *Builder[S, E] {
	b.transitions[transition[S, E]{from: from, event: event}] = to
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
	return newFSM(b.initial, b.transitions, b.cb)
}
