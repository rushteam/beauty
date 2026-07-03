// Package leveling 提供经验值 / 等级曲线原语:给定当前经验加一笔增量,算出新等级、
// 是否升级、跨了几级、当前等级内进度。纯计算、无状态、并发安全。
//
// 覆盖:角色升级、主播等级、VIP 等级、亲密度、公会等级——凡是"累计经验 → 等级"的
// 系统。等级曲线(每级所需经验)由调用方定义,本包只负责根据曲线做经验↔等级换算,
// 不预设任何数值,业务可自由用线性/多项式/查表曲线。
//
// 曲线抽象:Curve 是"等级 → 从 1 级到该级所需累计经验"的单调不减函数。
// 内置三种构造器:
//   - Linear:每级固定增量(等差);
//   - Poly:多项式增长(base * level^exp,常见的加速升级曲线);
//   - Table:查表(直接给出每级累计经验,最灵活,适合策划表)。
//
// 一次 Gain 返回完整结果(新等级/升了几级/当前等级内的经验与进度),
// 无内部状态——经验值由调用方持久化,本包做纯函数换算。零值不可用,用 New 构造。
package leveling

import "math"

// Curve 是等级曲线:CumulativeExp(level) 返回"达到 level 级"所需的累计经验
// (从 1 级起算,CumulativeExp(1)=0)。必须随 level 单调不减。
// MaxLevel 返回曲线支持的最高等级(满级)。
type Curve interface {
	CumulativeExp(level int) int64
	MaxLevel() int
}

// Result 一次经验变动后的结果快照。
type Result struct {
	Level      int   // 变动后的等级
	LeveledUp  bool  // 本次是否升级(至少 +1 级)
	LevelsGain int   // 本次跨越的等级数(可能一次升多级)
	TotalExp   int64 // 变动后的累计总经验
	CurExp     int64 // 当前等级内已获得的经验(距升级)
	NextExp    int64 // 当前等级升到下一级所需经验(满级为 0)
	IsMax      bool  // 是否已满级
}

// Leveler 经验/等级换算器。零值不可用,用 New 构造。无状态,并发安全。
type Leveler struct {
	curve Curve
}

// New 用给定曲线创建换算器。
func New(c Curve) *Leveler { return &Leveler{curve: c} }

// LevelAt 返回累计总经验 totalExp 对应的等级(夹在 [1, MaxLevel])。
func (l *Leveler) LevelAt(totalExp int64) int {
	if totalExp <= 0 {
		return 1
	}
	maxLv := l.curve.MaxLevel()
	// 找最大的 level 使 CumulativeExp(level) <= totalExp。曲线单调,用二分。
	lo, hi := 1, maxLv
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if l.curve.CumulativeExp(mid) <= totalExp {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// Gain 在累计总经验 totalExp 基础上增加 delta(delta 可为负,总经验夹到 >=0),
// 返回变动后的完整结果。满级后多余经验仍累计在 TotalExp 中但等级不再上升。
func (l *Leveler) Gain(totalExp, delta int64) Result {
	newTotal := max(totalExp+delta, 0)
	oldLevel := l.LevelAt(totalExp)
	return l.resultAt(newTotal, oldLevel)
}

// Stat 返回某累计总经验的当前状态(不改变经验,用于展示"当前 X 级,还差 Y 经验升级")。
func (l *Leveler) Stat(totalExp int64) Result {
	if totalExp < 0 {
		totalExp = 0
	}
	return l.resultAt(totalExp, l.LevelAt(totalExp))
}

func (l *Leveler) resultAt(newTotal int64, oldLevel int) Result {
	newLevel := l.LevelAt(newTotal)
	maxLv := l.curve.MaxLevel()
	isMax := newLevel >= maxLv

	base := l.curve.CumulativeExp(newLevel)
	r := Result{
		Level:      newLevel,
		LeveledUp:  newLevel > oldLevel,
		LevelsGain: newLevel - oldLevel,
		TotalExp:   newTotal,
		CurExp:     newTotal - base,
		IsMax:      isMax,
	}
	if !isMax {
		r.NextExp = l.curve.CumulativeExp(newLevel+1) - base
	}
	if r.LevelsGain < 0 {
		r.LevelsGain = 0 // 经验减少导致降级时,LeveledUp=false、LevelsGain 归零
		r.LeveledUp = false
	}
	return r
}

// Curve 返回底层曲线(便于查询 MaxLevel / 某级所需经验)。
func (l *Leveler) Curve() Curve { return l.curve }

// ===== 内置曲线 =====

// linearCurve 等差曲线:每级需要 perLevel 经验,满级 maxLevel。
type linearCurve struct {
	perLevel int64
	maxLevel int
}

// Linear 每级固定 perLevel 经验(1→2 需 perLevel,2→3 需 perLevel……)。
func Linear(perLevel int64, maxLevel int) Curve {
	if perLevel < 1 {
		perLevel = 1
	}
	if maxLevel < 1 {
		maxLevel = 1
	}
	return linearCurve{perLevel: perLevel, maxLevel: maxLevel}
}

func (c linearCurve) CumulativeExp(level int) int64 {
	if level <= 1 {
		return 0
	}
	if level > c.maxLevel {
		level = c.maxLevel
	}
	return int64(level-1) * c.perLevel
}
func (c linearCurve) MaxLevel() int { return c.maxLevel }

// tableCurve 查表曲线:cum[i] = 达到 (i+1) 级的累计经验,cum[0] 应为 0。
type tableCurve struct {
	cum []int64
}

// Table 用"每级累计经验表"构造曲线。cumulative[0] 是 1 级(应为 0),
// cumulative[i] 是 (i+1) 级的累计经验,须单调不减。MaxLevel = len(cumulative)。
// 适合直接对接策划数值表。传入空表则退化为 1 级满级。
func Table(cumulative []int64) Curve {
	if len(cumulative) == 0 {
		return tableCurve{cum: []int64{0}}
	}
	cp := make([]int64, len(cumulative))
	copy(cp, cumulative)
	cp[0] = 0 // 1 级累计经验强制为 0
	// 保证单调不减(防御性:后一项至少等于前一项)。
	for i := 1; i < len(cp); i++ {
		if cp[i] < cp[i-1] {
			cp[i] = cp[i-1]
		}
	}
	return tableCurve{cum: cp}
}

func (c tableCurve) CumulativeExp(level int) int64 {
	if level <= 1 {
		return 0
	}
	if level > len(c.cum) {
		level = len(c.cum)
	}
	return c.cum[level-1]
}
func (c tableCurve) MaxLevel() int { return len(c.cum) }

// polyCurve 多项式曲线:达到 level 级的累计经验 = base * (level-1)^exp(取整)。
type polyCurve struct {
	base     float64
	exp      float64
	maxLevel int
	cache    []int64 // 预算好的累计表(构造时算好,查询免浮点)
}

// Poly 多项式增长曲线:达到 level 级累计需 base*(level-1)^exp 经验
// (exp=2 是常见的二次加速曲线)。满级 maxLevel。
func Poly(base, exp float64, maxLevel int) Curve {
	if base <= 0 {
		base = 1
	}
	if exp < 1 {
		exp = 1
	}
	if maxLevel < 1 {
		maxLevel = 1
	}
	c := polyCurve{base: base, exp: exp, maxLevel: maxLevel}
	c.cache = make([]int64, maxLevel)
	for i := range maxLevel {
		c.cache[i] = int64(base * math.Pow(float64(i), exp))
	}
	// 单调化。
	for i := 1; i < maxLevel; i++ {
		if c.cache[i] < c.cache[i-1] {
			c.cache[i] = c.cache[i-1]
		}
	}
	return c
}

func (c polyCurve) CumulativeExp(level int) int64 {
	if level <= 1 {
		return 0
	}
	if level > c.maxLevel {
		level = c.maxLevel
	}
	return c.cache[level-1]
}
func (c polyCurve) MaxLevel() int { return c.maxLevel }
