// Package questlog 提供任务 / 成就的进度追踪:定义一组带目标值的任务,累加进度,
// 达标后可领取(一次),支持前置依赖与周期重置。纯内存、并发安全。
//
// 覆盖:每日任务、成就、活动进度、新手引导、通行证(battle pass)。这类系统的共性是
// "目标 + 进度 + 状态机(进行中→已达成→已领取)+ 依赖 + 重置",questlog 把它抽象成
// 一个原语,业务只需定义任务与推进进度,状态流转和领取幂等由本包保证。
//
// 与相邻原语的区别:
//   - counter 是"窗口内计数"(会过期、无目标/状态);questlog 是"朝目标累加 + 领取状态机",
//     进度不因时间流逝而减少(除非显式 Reset);
//   - fsm 是通用状态机;questlog 内建了任务专用的三态流转与依赖门控,无需自己接线。
//
// 模型:一个 Log 持有任务定义(所有 owner 共享)+ 每个 owner 的进度快照。
// 任务状态:Locked(前置未完成)< InProgress(进行中)< Achieved(达标待领)< Claimed(已领)。
// 进度推进用 Advance(按任务专属计数器累加)或 Set(直接设定当前值)。
//
// 泛型 ID 为任务标识(comparable)。并发安全(单锁,任务数与 owner 数中等规模适用)。
// 零值不可用,用 New 构造。
package questlog

import (
	"sync"
)

// Status 任务状态。
type Status int

const (
	// StatusLocked 前置任务未全部领取/达成,任务尚未解锁。
	StatusLocked Status = iota
	// StatusInProgress 已解锁,进度未达目标。
	StatusInProgress
	// StatusAchieved 进度已达目标,等待领取。
	StatusAchieved
	// StatusClaimed 已领取奖励(终态)。
	StatusClaimed
)

func (s Status) String() string {
	switch s {
	case StatusLocked:
		return "locked"
	case StatusInProgress:
		return "in_progress"
	case StatusAchieved:
		return "achieved"
	case StatusClaimed:
		return "claimed"
	default:
		return "unknown"
	}
}

// Quest 一个任务/成就的定义(所有 owner 共享)。
type Quest[ID comparable] struct {
	// ID 唯一标识。
	ID ID
	// Target 目标进度值(达到即视为完成,须 > 0)。
	Target int64
	// Requires 前置任务:这些任务全部 Claimed 后本任务才解锁(可空)。
	Requires []ID
	// Meta 业务自定义元数据(奖励内容、描述等),questlog 不解释。
	Meta any
}

// State 某 owner 在某任务上的状态快照。
type State[ID comparable] struct {
	ID       ID
	Status   Status
	Progress int64 // 当前进度(<= Target)
	Target   int64
}

// ownerProg 单个 owner 的进度存储:任务 ID → 当前进度;以及已领取集合。
type ownerProg[ID comparable] struct {
	progress map[ID]int64
	claimed  map[ID]bool
}

// Log 任务日志:持有任务定义 + 各 owner 进度。零值不可用,用 New 构造。并发安全。
type Log[ID comparable] struct {
	mu      sync.Mutex
	quests  map[ID]Quest[ID]
	order   []ID // 定义顺序(用于稳定遍历)
	owners  map[string]*ownerProg[ID]
	onClaim func(owner string, q Quest[ID]) // 可选:领取回调(发奖用)
}

// Option 配置 Log。
type Option[ID comparable] func(*Log[ID])

// WithOnClaim 设置领取回调:Claim 成功时触发(用于发奖/打点)。回调在锁外执行。
func WithOnClaim[ID comparable](fn func(owner string, q Quest[ID])) Option[ID] {
	return func(l *Log[ID]) { l.onClaim = fn }
}

// New 创建任务日志并注册任务定义。重复 ID 后者覆盖前者。
func New[ID comparable](quests []Quest[ID], opts ...Option[ID]) *Log[ID] {
	l := &Log[ID]{
		quests: make(map[ID]Quest[ID], len(quests)),
		owners: make(map[string]*ownerProg[ID]),
	}
	for _, o := range opts {
		o(l)
	}
	for _, q := range quests {
		if _, ok := l.quests[q.ID]; !ok {
			l.order = append(l.order, q.ID)
		}
		l.quests[q.ID] = q
	}
	return l
}

func (l *Log[ID]) ownerLocked(owner string) *ownerProg[ID] {
	op := l.owners[owner]
	if op == nil {
		op = &ownerProg[ID]{progress: make(map[ID]int64), claimed: make(map[ID]bool)}
		l.owners[owner] = op
	}
	return op
}

// unlockedLocked 判断任务前置是否全部满足(依赖任务均已 Claimed)。
func (l *Log[ID]) unlockedLocked(op *ownerProg[ID], q Quest[ID]) bool {
	for _, req := range q.Requires {
		if !op.claimed[req] {
			return false
		}
	}
	return true
}

// statusLocked 计算某任务的当前状态。
func (l *Log[ID]) statusLocked(op *ownerProg[ID], q Quest[ID]) Status {
	if op.claimed[q.ID] {
		return StatusClaimed
	}
	if !l.unlockedLocked(op, q) {
		return StatusLocked
	}
	if op.progress[q.ID] >= q.Target {
		return StatusAchieved
	}
	return StatusInProgress
}

// Advance 给 owner 的任务 id 累加 delta 进度(delta 可为负,进度夹在 [0, Target])。
// 任务不存在或被锁定(前置未完成)时不生效。返回推进后的状态快照与是否发生变化。
func (l *Log[ID]) Advance(owner string, id ID, delta int64) (State[ID], bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	q, ok := l.quests[id]
	if !ok {
		return State[ID]{}, false
	}
	op := l.ownerLocked(owner)
	if op.claimed[id] || !l.unlockedLocked(op, q) {
		return l.snapshotLocked(op, q), false // 已领或未解锁,进度不动
	}
	cur := op.progress[id]
	next := min(max(cur+delta, 0), q.Target)
	op.progress[id] = next
	return l.snapshotLocked(op, q), next != cur
}

// Set 直接设定 owner 的任务 id 进度为 v(夹在 [0, Target])。语义同 Advance 的门控。
func (l *Log[ID]) Set(owner string, id ID, v int64) (State[ID], bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	q, ok := l.quests[id]
	if !ok {
		return State[ID]{}, false
	}
	op := l.ownerLocked(owner)
	if op.claimed[id] || !l.unlockedLocked(op, q) {
		return l.snapshotLocked(op, q), false
	}
	if v < 0 {
		v = 0
	}
	if v > q.Target {
		v = q.Target
	}
	cur := op.progress[id]
	op.progress[id] = v
	return l.snapshotLocked(op, q), v != cur
}

// Claim 领取 owner 的任务 id 奖励。仅当状态为 Achieved 时成功(返回 true),
// 幂等:已领取或未达标返回 false。成功时触发 OnClaim 回调(锁外)。
func (l *Log[ID]) Claim(owner string, id ID) bool {
	l.mu.Lock()
	q, ok := l.quests[id]
	if !ok {
		l.mu.Unlock()
		return false
	}
	op := l.ownerLocked(owner)
	if l.statusLocked(op, q) != StatusAchieved {
		l.mu.Unlock()
		return false
	}
	op.claimed[id] = true
	fn := l.onClaim
	l.mu.Unlock()

	if fn != nil {
		fn(owner, q)
	}
	return true
}

// ClaimAll 领取 owner 所有当前可领(Achieved)的任务,返回被领取的任务 ID 列表(按定义顺序)。
func (l *Log[ID]) ClaimAll(owner string) []ID {
	l.mu.Lock()
	op := l.ownerLocked(owner)
	var claimed []ID
	var quests []Quest[ID]
	for _, id := range l.order {
		q := l.quests[id]
		if l.statusLocked(op, q) == StatusAchieved {
			op.claimed[id] = true
			claimed = append(claimed, id)
			quests = append(quests, q)
		}
	}
	fn := l.onClaim
	l.mu.Unlock()

	if fn != nil {
		for _, q := range quests {
			fn(owner, q)
		}
	}
	return claimed
}

// StateOf 返回 owner 在任务 id 上的状态快照。任务不存在返回零值 + false。
func (l *Log[ID]) StateOf(owner string, id ID) (State[ID], bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	q, ok := l.quests[id]
	if !ok {
		return State[ID]{}, false
	}
	return l.snapshotLocked(l.ownerLocked(owner), q), true
}

// List 返回 owner 所有任务的状态快照(按定义顺序)。
func (l *Log[ID]) List(owner string) []State[ID] {
	l.mu.Lock()
	defer l.mu.Unlock()
	op := l.ownerLocked(owner)
	out := make([]State[ID], 0, len(l.order))
	for _, id := range l.order {
		out = append(out, l.snapshotLocked(op, l.quests[id]))
	}
	return out
}

// Claimable 返回 owner 当前可领取(Achieved)的任务 ID(按定义顺序)。用于小红点/领取提示。
func (l *Log[ID]) Claimable(owner string) []ID {
	l.mu.Lock()
	defer l.mu.Unlock()
	op := l.ownerLocked(owner)
	var out []ID
	for _, id := range l.order {
		if l.statusLocked(op, l.quests[id]) == StatusAchieved {
			out = append(out, id)
		}
	}
	return out
}

// Reset 重置 owner 指定任务的进度与领取状态(周期任务刷新用)。ids 为空则重置全部。
func (l *Log[ID]) Reset(owner string, ids ...ID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	op := l.owners[owner]
	if op == nil {
		return
	}
	if len(ids) == 0 {
		op.progress = make(map[ID]int64)
		op.claimed = make(map[ID]bool)
		return
	}
	for _, id := range ids {
		delete(op.progress, id)
		delete(op.claimed, id)
	}
}

func (l *Log[ID]) snapshotLocked(op *ownerProg[ID], q Quest[ID]) State[ID] {
	p := min(op.progress[q.ID], q.Target)
	return State[ID]{ID: q.ID, Status: l.statusLocked(op, q), Progress: p, Target: q.Target}
}
