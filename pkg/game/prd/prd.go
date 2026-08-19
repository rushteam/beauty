// Package prd 提供伪随机概率补偿(Pseudo-Random Distribution)算法。
//
// 解决的问题:真随机在小样本下体验极差——25% 暴击率的角色可能连续 10 刀不暴击,
// 也可能连续 3 刀暴击,玩家感知不到"公平"。PRD 通过动态调整每次触发概率,使得:
//   - 连续不触发越久,下次触发概率越高(保底感);
//   - 一旦触发立即重置,避免连续触发(避免爆发不公);
//   - 长期触发率精确等于标称概率 P。
//
// 数学原理:
//
//	第 N 次尝试的触发概率 = N × C
//	其中 C 是一个由标称概率 P 反解出的常数(满足期望触发次数 = 1/P)。
//	当 N×C ≥ 1 时保证触发(硬保底)。
//
// 典型应用:MOBA 暴击率、闪避率、触发型被动技能、怪物掉落。
//
// PRD 与 loot.Puller 的区别:Puller 是硬保底计数器(到次数必出),PRD 是
// 概率递增的"软保底"——体感更自然,且严格保持标称概率。
//
// Roller 有内部状态,非并发安全(每角色一个,或自行加锁)。
package prd

import "math/rand/v2"

// cTable 预计算的 C 值表(P 从 0.01 到 1.00,步长 0.01)。
// C(P) 的精确解需要数值迭代,这里用启动时初始化的查找表 + 线性插值。
var cTable [101]float64

func init() {
	for i := 1; i <= 100; i++ {
		p := float64(i) / 100.0
		cTable[i] = computeC(p)
	}
}

// computeC 通过二分法求解满足 actualP(C) = P 的常数 C。
func computeC(p float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	lo, hi := 0.0, p
	for range 64 {
		mid := (lo + hi) / 2
		if actualP(mid) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// actualP 给定 C,计算实际触发概率 = 1 / E[尝试次数直到触发]。
// E[N] = Σ(n=1..maxN) n × P(恰好在第 n 次触发)
// P(恰好在第 n 次触发) = min(n*C, 1) × Π(k=1..n-1)(1 - min(k*C, 1))
func actualP(c float64) float64 {
	if c <= 0 {
		return 0
	}
	maxN := int(1.0/c) + 1
	if maxN > 500 {
		maxN = 500
	}
	expectedN := 0.0
	survivalProb := 1.0 // Π(k=1..n-1)(1 - k*C)
	for n := 1; n <= maxN; n++ {
		triggerProb := float64(n) * c
		if triggerProb > 1 {
			triggerProb = 1
		}
		expectedN += float64(n) * triggerProb * survivalProb
		survivalProb *= (1 - triggerProb)
		if survivalProb < 1e-15 {
			break
		}
	}
	if expectedN <= 0 {
		return 0
	}
	return 1.0 / expectedN
}

// CFromP 返回标称概率 P 对应的 PRD 常数 C。P 应在 (0, 1] 之间。
// P <= 0 返回 0;P >= 1 返回 1。
func CFromP(p float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	idx := p * 100
	lo := int(idx)
	if lo >= 100 {
		return cTable[100]
	}
	if lo < 1 {
		frac := p / 0.01
		return cTable[1] * frac
	}
	frac := idx - float64(lo)
	return cTable[lo]*(1-frac) + cTable[lo+1]*frac
}

// MaxN 返回标称概率 P 下的最大保底次数(N×C ≥ 1 时必触发)。
func MaxN(p float64) int {
	c := CFromP(p)
	if c <= 0 {
		return 0
	}
	return int(1.0/c) + 1
}

// Roller 是一个有状态的 PRD 判定器。每个角色/技能持有一个实例。
// 非并发安全。
type Roller struct {
	c     float64
	count int // 连续未触发次数
	rng   *rand.Rand
}

// Option 配置 Roller。
type Option func(*Roller)

// WithRand 指定随机源(用于可复现测试)。默认用 math/rand/v2 全局源。
func WithRand(r *rand.Rand) Option {
	return func(ro *Roller) { ro.rng = r }
}

// New 创建 PRD 判定器。p 为标称触发概率,如 0.25 表示 25% 暴击率。
func New(p float64, opts ...Option) *Roller {
	r := &Roller{c: CFromP(p)}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Roll 执行一次判定:返回是否触发。触发后内部计数重置。
func (r *Roller) Roll() bool {
	r.count++
	prob := float64(r.count) * r.c
	if prob >= 1 {
		r.count = 0
		return true
	}
	rnd := r.f64()
	if rnd < prob {
		r.count = 0
		return true
	}
	return false
}

// Reset 重置内部计数(如角色死亡/回合切换时调用)。
func (r *Roller) Reset() { r.count = 0 }

// Count 返回当前连续未触发次数。
func (r *Roller) Count() int { return r.count }

// NextProb 返回下一次判定的触发概率(用于 UI 提示)。
func (r *Roller) NextProb() float64 {
	p := float64(r.count+1) * r.c
	if p > 1 {
		return 1
	}
	return p
}

func (r *Roller) f64() float64 {
	if r.rng != nil {
		return r.rng.Float64()
	}
	return rand.Float64()
}
