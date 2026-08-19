// Package behaviortree 提供泛型行为树原语,用于游戏 AI 决策(小兵/怪物/NPC)。
//
// 解决的问题:FSM 处理线性状态转换简洁,但复杂 AI(MOBA 小兵"追击→攻击→
// 撤退→巡逻"且可被打断)用 FSM 会变成意大利面状的状态爆炸。行为树通过树形
// 结构组合「条件检查」和「动作执行」,天然支持优先级打断与多帧异步任务。
//
// 与相邻原语的关系:
//   - fsm 适合「生命周期状态流转」(房间、订单),行为树适合「每帧 AI 决策」;
//   - gameloop/match 的 OnTick 内对每个 AI 实体调用 root.Tick(ctx);
//   - 行为树不管 tick 调度——它是纯同步的"给定上下文,返回状态",调度在 gameloop。
//
// 核心设计(机制而非策略):
//   - 三种状态:Running(进行中)、Success(成功)、Failure(失败);
//   - 四种节点:Sequence(顺序)、Selector(选择)、Parallel(并行)、Decorator(装饰);
//   - 叶子节点由业务定义(Action/Condition),本包只提供组合机制;
//   - 泛型参数 T 是黑板(Blackboard)类型——AI 的共享上下文(目标位置、血量等),
//     业务自定义结构体,行为树只透传。
//
// 并发安全:Tick 本身不加锁(每个 AI 实体独立调用);节点定义(树结构)构建后只读。
// 零值不可用:用 Sequence/Selector/Parallel 等构造函数组装树。
package behaviortree

// Status 是行为树节点的执行结果。
type Status int

const (
	// Running 表示节点尚未完成,下次 Tick 继续执行。
	Running Status = iota
	// Success 表示节点成功完成。
	Success
	// Failure 表示节点执行失败。
	Failure
)

func (s Status) String() string {
	switch s {
	case Running:
		return "Running"
	case Success:
		return "Success"
	case Failure:
		return "Failure"
	default:
		return "Unknown"
	}
}

// Node 是行为树的节点接口。T 是黑板类型(业务自定义的 AI 上下文)。
type Node[T any] interface {
	Tick(bb *T) Status
}

// ---------- 叶子节点 ----------

// Action 把普通函数适配为叶子节点。函数返回 Status 以支持多帧任务(Running)。
type Action[T any] func(bb *T) Status

func (a Action[T]) Tick(bb *T) Status { return a(bb) }

// Condition 把布尔判断适配为叶子节点:true → Success,false → Failure。
type Condition[T any] func(bb *T) bool

func (c Condition[T]) Tick(bb *T) Status {
	if c(bb) {
		return Success
	}
	return Failure
}

// ---------- 组合节点 ----------

// Sequence 顺序执行子节点:依次 Tick 每个子节点,任一返回 Failure 则立即返回
// Failure;任一返回 Running 则返回 Running(下次 Tick 从头开始);全部 Success
// 则返回 Success。语义等价逻辑 AND。
//
// 注意:无记忆(stateless)——每次 Tick 从第一个子节点重新开始。
// 需要"从上次 Running 处继续"的场景用 MemSequence。
func Sequence[T any](children ...Node[T]) Node[T] {
	return &sequence[T]{children: children}
}

type sequence[T any] struct{ children []Node[T] }

func (s *sequence[T]) Tick(bb *T) Status {
	for _, c := range s.children {
		switch c.Tick(bb) {
		case Failure:
			return Failure
		case Running:
			return Running
		}
	}
	return Success
}

// Selector 选择执行子节点:依次 Tick 每个子节点,任一返回 Success 则立即返回
// Success;任一返回 Running 则返回 Running;全部 Failure 则返回 Failure。
// 语义等价逻辑 OR(优先级选择)。
func Selector[T any](children ...Node[T]) Node[T] {
	return &selector[T]{children: children}
}

type selector[T any] struct{ children []Node[T] }

func (s *selector[T]) Tick(bb *T) Status {
	for _, c := range s.children {
		switch c.Tick(bb) {
		case Success:
			return Success
		case Running:
			return Running
		}
	}
	return Failure
}

// MemSequence 记忆型顺序节点:从上次返回 Running 的子节点继续,而非每次从头。
// 适合多帧动画/寻路等需要跨 Tick 连续执行的场景。
// 非并发安全(每个 AI 实体独占)。
func MemSequence[T any](children ...Node[T]) Node[T] {
	return &memSequence[T]{children: children}
}

type memSequence[T any] struct {
	children []Node[T]
	running  int
}

func (s *memSequence[T]) Tick(bb *T) Status {
	for i := s.running; i < len(s.children); i++ {
		switch s.children[i].Tick(bb) {
		case Failure:
			s.running = 0
			return Failure
		case Running:
			s.running = i
			return Running
		}
	}
	s.running = 0
	return Success
}

// MemSelector 记忆型选择节点:从上次返回 Running 的子节点继续。
func MemSelector[T any](children ...Node[T]) Node[T] {
	return &memSelector[T]{children: children}
}

type memSelector[T any] struct {
	children []Node[T]
	running  int
}

func (s *memSelector[T]) Tick(bb *T) Status {
	for i := s.running; i < len(s.children); i++ {
		switch s.children[i].Tick(bb) {
		case Success:
			s.running = 0
			return Success
		case Running:
			s.running = i
			return Running
		}
	}
	s.running = 0
	return Failure
}

// ParallelPolicy 定义 Parallel 节点的成功/失败判定策略。
type ParallelPolicy int

const (
	// RequireAll 所有子节点 Success 才 Success(任一 Failure 即 Failure)。
	RequireAll ParallelPolicy = iota
	// RequireOne 任一子节点 Success 即 Success(全部 Failure 才 Failure)。
	RequireOne
)

// Parallel 并行执行所有子节点(每次 Tick 全部都执行),按 policy 判定结果。
// 常用于"一边移动一边攻击"等并发行为。
func Parallel[T any](policy ParallelPolicy, children ...Node[T]) Node[T] {
	return &parallel[T]{policy: policy, children: children}
}

type parallel[T any] struct {
	policy   ParallelPolicy
	children []Node[T]
}

func (p *parallel[T]) Tick(bb *T) Status {
	var successes, failures int
	for _, c := range p.children {
		switch c.Tick(bb) {
		case Success:
			successes++
		case Failure:
			failures++
		}
	}
	total := len(p.children)
	switch p.policy {
	case RequireAll:
		if failures > 0 {
			return Failure
		}
		if successes == total {
			return Success
		}
		return Running
	case RequireOne:
		if successes > 0 {
			return Success
		}
		if failures == total {
			return Failure
		}
		return Running
	default:
		return Failure
	}
}

// ---------- 装饰节点 ----------

// Inverter 取反:子节点 Success → Failure,Failure → Success,Running 不变。
func Inverter[T any](child Node[T]) Node[T] {
	return &inverter[T]{child: child}
}

type inverter[T any] struct{ child Node[T] }

func (i *inverter[T]) Tick(bb *T) Status {
	switch i.child.Tick(bb) {
	case Success:
		return Failure
	case Failure:
		return Success
	default:
		return Running
	}
}

// Succeeder 强制成功:无论子节点返回什么,都返回 Success(Running 除外)。
func Succeeder[T any](child Node[T]) Node[T] {
	return &succeeder[T]{child: child}
}

type succeeder[T any] struct{ child Node[T] }

func (s *succeeder[T]) Tick(bb *T) Status {
	if s.child.Tick(bb) == Running {
		return Running
	}
	return Success
}

// Repeater 重复执行子节点 n 次。子节点返回 Failure 时提前中止并返回 Failure;
// 子节点返回 Running 时暂停本次迭代,下次 Tick 继续当前迭代。
// n <= 0 表示无限重复(只能被 Failure 中止)。
// 非并发安全(每个 AI 实体独占)。
func Repeater[T any](n int, child Node[T]) Node[T] {
	return &repeater[T]{limit: n, child: child}
}

type repeater[T any] struct {
	limit int
	count int
	child Node[T]
}

func (r *repeater[T]) Tick(bb *T) Status {
	for {
		if r.limit > 0 && r.count >= r.limit {
			r.count = 0
			return Success
		}
		switch r.child.Tick(bb) {
		case Failure:
			r.count = 0
			return Failure
		case Running:
			return Running
		default:
			r.count++
		}
	}
}

// RepeatUntilFail 重复执行子节点直到返回 Failure,然后返回 Success。
// 子节点返回 Running 时暂停,下次 Tick 继续。
func RepeatUntilFail[T any](child Node[T]) Node[T] {
	return &repeatUntilFail[T]{child: child}
}

type repeatUntilFail[T any] struct{ child Node[T] }

func (r *repeatUntilFail[T]) Tick(bb *T) Status {
	switch r.child.Tick(bb) {
	case Failure:
		return Success
	case Running:
		return Running
	default:
		return Running
	}
}

// Guard 守卫节点:先检查条件,条件通过才执行子节点;条件失败直接返回 Failure。
// 等价 Sequence(condition, child),但语义更清晰。
func Guard[T any](cond func(bb *T) bool, child Node[T]) Node[T] {
	return &guard[T]{cond: cond, child: child}
}

type guard[T any] struct {
	cond  func(bb *T) bool
	child Node[T]
}

func (g *guard[T]) Tick(bb *T) Status {
	if !g.cond(bb) {
		return Failure
	}
	return g.child.Tick(bb)
}

// Cooldown 冷却装饰器:子节点执行成功后,在 ticks 次 Tick 内跳过执行(返回 Failure)。
// 适合"技能 CD"或"巡逻间隔"等需要冷却的 AI 行为。
// 非并发安全(每个 AI 实体独占)。
func Cooldown[T any](ticks int, child Node[T]) Node[T] {
	return &cooldownNode[T]{ticks: ticks, child: child}
}

type cooldownNode[T any] struct {
	ticks   int
	counter int
	child   Node[T]
}

func (c *cooldownNode[T]) Tick(bb *T) Status {
	if c.counter > 0 {
		c.counter--
		return Failure
	}
	s := c.child.Tick(bb)
	if s == Success {
		c.counter = c.ticks
	}
	return s
}
