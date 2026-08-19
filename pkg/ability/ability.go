// Package ability 提供泛型技能/效果管线(Ability Pipeline)原语,类似虚幻引擎的 GAS
// (Gameplay Ability System)的核心骨架,但遵循"机制而非策略"——只提供管线调度机制,
// 不绑定具体伤害公式/buff 类型/特效系统。
//
// 解决的问题:MOBA/RPG/ACT 类游戏中,技能数量常达数百个,但底层运作模式高度一致:
// "判定前置条件 → 扣资源 → 分阶段执行(蓄力/施法/释放) → 产出效果(伤害/buff)
// → 发送表现提示(特效/音效)"。没有统一管线时,每个技能各写一套 if/else,维护成本
// 爆炸且策划无法配表驱动。
//
// 核心管线:
//
//	Ability 定义                     运行时
//	┌──────────┐                   ┌──────────────┐
//	│ CanUse?  │──检查前置──────────▶│ 资源扣除     │
//	│ Cost     │                   │              │
//	│ Phases[] │──按序执行──────────▶│ Phase.Tick() │──▶ PhaseResult
//	│          │                   │   .Effects   │      │
//	│          │                   │   .Cues      │      ├─ Effect (业务消费)
//	│          │                   │   .Status    │      └─ Cue    (客户端消费)
//	└──────────┘                   └──────────────┘
//
// 泛型参数:
//   - E: 实体类型(施法者/目标,业务定义);
//   - R: 资源类型(蓝量/怒气/弹药,业务定义)。
//
// 与相邻原语的关系:
//   - gameloop/match 的 OnTick 中调用 Runner.Tick(frame) 驱动管线;
//   - fsm 管理实体的宏观状态(正常/眩晕/死亡),ability 管理"施法中"的微观阶段;
//   - cooldown 可用于管线外的技能冷却追踪,但 Runner 内置了 CD 管理;
//   - fixedpoint 可用于 Effect 的伤害公式计算以保证确定性;
//   - eventbus 消费 Effect/Cue(如任务系统监听"造成伤害"事件)。
//
// 并发安全:Runner 不加锁(由单个游戏循环驱动,每实体一个)。
// 零值不可用:用 NewRunner 构造。
package ability

// ---------- 核心类型 ----------

// Effect 是技能产出的逻辑效果(伤害、治疗、buff/debuff、位移等)。
// 由业务层消费:修改 HP、应用 buff、触发事件等。
// Tag 用于分类(如 "damage"、"heal"、"stun"),Value 是效果量(含义由 Tag 决定),
// Source/Target 标识来源与目标。
type Effect[E any] struct {
	Tag    string  // 效果类型标签
	Value  float64 // 效果数值(伤害值、治疗量、持续时间等)
	Source E       // 施法者
	Target E       // 受击者
}

// Cue 是客户端表现提示(特效、音效、镜头震动)。
// 服务器产出后广播给客户端,客户端据此播放表现,不影响逻辑状态。
type Cue struct {
	Tag   string  // 提示类型(如 "vfx"、"sfx"、"camera_shake")
	Asset string  // 资源标识(特效/音效文件名)
	Value float64 // 参数(强度、持续时间等)
}

// PhaseStatus 是阶段的执行状态。
type PhaseStatus int

const (
	// PhaseRunning 阶段尚在进行中,下次 Tick 继续。
	PhaseRunning PhaseStatus = iota
	// PhaseComplete 阶段已完成,管线推进到下一阶段。
	PhaseComplete
	// PhaseCancelled 阶段被取消(打断/眩晕),整个技能中止。
	PhaseCancelled
)

// PhaseResult 是阶段单次 Tick 的产出。
type PhaseResult[E any] struct {
	Status  PhaseStatus
	Effects []Effect[E]
	Cues    []Cue
}

// Phase 是技能执行的一个阶段。业务实现此接口定义具体行为。
// Tick 每帧被调用(Running 期间),frame 是全局帧号。
// 管线保证同一时刻只有一个 Phase 在 Tick。
type Phase[E any] interface {
	// Tick 推进阶段。返回 PhaseComplete 时管线自动进入下一阶段;
	// 返回 PhaseCancelled 时整个技能中止。
	Tick(frame uint64) PhaseResult[E]
	// Reset 重置阶段内部状态(技能再次使用时调用)。
	Reset()
}

// PhaseFunc 把普通函数适配为 Phase(无状态阶段的简写)。
type PhaseFunc[E any] func(frame uint64) PhaseResult[E]

func (f PhaseFunc[E]) Tick(frame uint64) PhaseResult[E] { return f(frame) }
func (f PhaseFunc[E]) Reset()                           {}

// Cost 是技能的资源消耗定义。
type Cost[R any] struct {
	Resource R       // 资源类型标识(如 MP/Rage/Ammo)
	Amount   float64 // 消耗量
}

// Spec 是技能的声明式定义(数据驱动,策划可配表)。
type Spec[E, R any] struct {
	ID           string     // 技能唯一标识
	Costs        []Cost[R]  // 资源消耗
	CooldownTick int        // 冷却帧数(0 = 无冷却)
	Phases       []Phase[E] // 执行阶段序列
}

// ---------- Runner 运行时 ----------

// ResourceChecker 检查并扣除资源(业务实现)。
type ResourceChecker[E, R any] interface {
	// CanAfford 检查 caster 是否有足够资源支付 costs。
	CanAfford(caster E, costs []Cost[R]) bool
	// Spend 扣除资源。调用方保证已通过 CanAfford 检查。
	Spend(caster E, costs []Cost[R])
}

// ResourceFunc 用两个函数适配 ResourceChecker。
type ResourceFunc[E, R any] struct {
	CheckFn func(caster E, costs []Cost[R]) bool
	SpendFn func(caster E, costs []Cost[R])
}

func (r ResourceFunc[E, R]) CanAfford(caster E, costs []Cost[R]) bool {
	if r.CheckFn == nil {
		return true
	}
	return r.CheckFn(caster, costs)
}

func (r ResourceFunc[E, R]) Spend(caster E, costs []Cost[R]) {
	if r.SpendFn != nil {
		r.SpendFn(caster, costs)
	}
}

// CastState 施法状态。
type CastState int

const (
	// Idle 空闲,可施放新技能。
	Idle CastState = iota
	// Casting 施法中。
	Casting
	// CooldownState 冷却中(技能刚结束)。
	CooldownState
)

// Runner 为单个实体管理技能施放生命周期:冷却 → 资源检查 → 分阶段执行。
// 每帧调用 Tick(frame) 驱动。非并发安全(每实体一个,由游戏循环串行驱动)。
type Runner[E, R any] struct {
	caster   E
	res      ResourceChecker[E, R]
	state    CastState
	current  *Spec[E, R]
	phaseIdx int
	// 冷却管理:skillID → 冷却结束帧号
	cooldowns map[string]uint64
}

// NewRunner 创建技能管线运行时。caster 是施法者实体,res 是资源检查器。
func NewRunner[E, R any](caster E, res ResourceChecker[E, R]) *Runner[E, R] {
	return &Runner[E, R]{
		caster:    caster,
		res:       res,
		cooldowns: make(map[string]uint64),
	}
}

// CastError 施法失败原因。
type CastError int

const (
	ErrNone       CastError = iota
	ErrBusy                 // 正在施法中
	ErrOnCooldown           // 冷却中
	ErrNoResource           // 资源不足
	ErrNoPhases             // 技能无阶段定义
)

func (e CastError) Error() string {
	switch e {
	case ErrBusy:
		return "ability: caster is busy"
	case ErrOnCooldown:
		return "ability: skill on cooldown"
	case ErrNoResource:
		return "ability: insufficient resources"
	case ErrNoPhases:
		return "ability: skill has no phases"
	default:
		return "ability: unknown error"
	}
}

// Cast 尝试施放技能。成功则进入 Casting 状态(后续 Tick 驱动阶段执行)。
// frame 是当前帧号,用于冷却判定。失败返回 CastError。
func (r *Runner[E, R]) Cast(skill *Spec[E, R], frame uint64) error {
	if r.state == Casting {
		return ErrBusy
	}
	if len(skill.Phases) == 0 {
		return ErrNoPhases
	}
	if cdEnd, ok := r.cooldowns[skill.ID]; ok && frame < cdEnd {
		return ErrOnCooldown
	}
	if !r.res.CanAfford(r.caster, skill.Costs) {
		return ErrNoResource
	}
	r.res.Spend(r.caster, skill.Costs)
	for _, p := range skill.Phases {
		p.Reset()
	}
	r.current = skill
	r.phaseIdx = 0
	r.state = Casting
	return nil
}

// TickResult 是一帧管线的产出。
type TickResult[E any] struct {
	Active  bool // 是否有技能在执行
	Effects []Effect[E]
	Cues    []Cue
	Done    bool // 本帧技能完成(所有阶段结束)
	Cancel  bool // 本帧技能被取消
}

// Tick 驱动管线一帧。由游戏循环每帧调用。
func (r *Runner[E, R]) Tick(frame uint64) TickResult[E] {
	if r.state != Casting || r.current == nil {
		return TickResult[E]{}
	}
	phase := r.current.Phases[r.phaseIdx]
	pr := phase.Tick(frame)

	result := TickResult[E]{
		Active:  true,
		Effects: pr.Effects,
		Cues:    pr.Cues,
	}

	switch pr.Status {
	case PhaseComplete:
		r.phaseIdx++
		if r.phaseIdx >= len(r.current.Phases) {
			r.finishCast(frame)
			result.Done = true
		}
	case PhaseCancelled:
		r.cancelCast()
		result.Cancel = true
	}
	return result
}

// Cancel 外部打断当前施法(眩晕/位移/死亡)。
func (r *Runner[E, R]) Cancel() {
	if r.state == Casting {
		r.cancelCast()
	}
}

// State 返回当前施法状态。
func (r *Runner[E, R]) State() CastState { return r.state }

// IsIdle 是否空闲。
func (r *Runner[E, R]) IsIdle() bool { return r.state == Idle }

// CurrentSkill 返回当前施放的技能 ID(空闲时为空)。
func (r *Runner[E, R]) CurrentSkill() string {
	if r.current != nil && r.state == Casting {
		return r.current.ID
	}
	return ""
}

// CooldownRemaining 返回技能剩余冷却帧数(无冷却或已就绪返回 0)。
func (r *Runner[E, R]) CooldownRemaining(skillID string, frame uint64) int {
	cdEnd, ok := r.cooldowns[skillID]
	if !ok || frame >= cdEnd {
		return 0
	}
	return int(cdEnd - frame)
}

func (r *Runner[E, R]) finishCast(frame uint64) {
	if r.current.CooldownTick > 0 {
		r.cooldowns[r.current.ID] = frame + uint64(r.current.CooldownTick)
	}
	r.current = nil
	r.phaseIdx = 0
	r.state = Idle
}

func (r *Runner[E, R]) cancelCast() {
	r.current = nil
	r.phaseIdx = 0
	r.state = Idle
}
