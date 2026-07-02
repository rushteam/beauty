// Package loot 提供加权随机抽取原语:抽卡 / 开宝箱 / 怪物掉落 / 直播抽奖。
//
// 核心是 Table[T]:每个物品带权重,按权重比例随机抽取。抽取用 Vose's Alias Method,
// 预处理 O(n) 建两张表后,每次抽取 O(1)(而非朴素前缀和 + 二分的 O(log n)),
// 海量抽卡场景下这是关键。
//
// 三种进阶能力(可选):
//   - 保底(pity):Puller 记录"连续多少次没出某稀有度",达阈值强制出一个该稀有度
//     物品——几乎所有商业抽卡都有的机制,避免非酋无限空手;
//   - 不放回抽取(DrawN):一次抽 N 个不重复(十连抽的去重语义);
//   - 有状态抽取器(Puller):把 pity 计数等状态封装,每个玩家一个。
//
// Table 构建后只读、并发安全(Alias 表不可变);随机源默认 math/rand/v2(并发安全,
// 免播种);Puller 有内部状态,非并发安全(每玩家一个,天然隔离,或自行加锁)。
//
// 零值不可用:Table 用 NewTable 构造。
package loot

import (
	"errors"
	"math/rand/v2"
)

// Item 是一个可被抽取的物品:携带业务值 Value、非负权重 Weight、可选稀有度 Rarity。
// Rarity 用于保底(pity)判定;不使用保底时可忽略。
type Item[T any] struct {
	Value  T
	Weight float64
	Rarity int // 稀有度等级(越大越稀有),仅用于 pity;默认 0
}

// Table 是一张加权抽取表。构建后只读、并发安全。零值不可用,用 NewTable 构造。
type Table[T any] struct {
	items  []Item[T]
	prob   []float64 // Alias 表:落在本桶的概率
	alias  []int     // Alias 表:溢出时跳转的桶下标
	totalW float64
	rng    *rand.Rand // nil 表示用全局 rand(并发安全)
}

// Option 配置 Table。
type Option[T any] func(*Table[T])

// WithRand 指定随机源(用于可复现测试)。默认用 math/rand/v2 全局源(并发安全)。
// 注意:自定义 *rand.Rand 非并发安全,若 Table 并发抽取请勿使用,或自行加锁。
func WithRand[T any](r *rand.Rand) Option[T] {
	return func(t *Table[T]) { t.rng = r }
}

// ErrEmptyTable 表内无有效物品(全部权重 <=0 或列表为空)。
var ErrEmptyTable = errors.New("loot: table has no positive-weight items")

// NewTable 用一组物品构建抽取表。权重 <=0 的物品被忽略。
// 无有效物品时返回 ErrEmptyTable。
func NewTable[T any](items []Item[T], opts ...Option[T]) (*Table[T], error) {
	t := &Table[T]{}
	for _, o := range opts {
		o(t)
	}
	for _, it := range items {
		if it.Weight > 0 {
			t.items = append(t.items, it)
			t.totalW += it.Weight
		}
	}
	n := len(t.items)
	if n == 0 {
		return nil, ErrEmptyTable
	}
	t.buildAlias()
	return t, nil
}

// buildAlias 用 Vose's Alias Method 构建 prob/alias 表(O(n))。
func (t *Table[T]) buildAlias() {
	n := len(t.items)
	t.prob = make([]float64, n)
	t.alias = make([]int, n)

	// 归一化到均值为 1 的 scaled 概率(每桶目标概率 = 1/n)。
	scaled := make([]float64, n)
	for i, it := range t.items {
		scaled[i] = it.Weight / t.totalW * float64(n)
	}

	small := make([]int, 0, n) // scaled < 1
	large := make([]int, 0, n) // scaled >= 1
	for i, p := range scaled {
		if p < 1 {
			small = append(small, i)
		} else {
			large = append(large, i)
		}
	}

	for len(small) > 0 && len(large) > 0 {
		s := small[len(small)-1]
		small = small[:len(small)-1]
		l := large[len(large)-1]
		large = large[:len(large)-1]

		t.prob[s] = scaled[s]
		t.alias[s] = l
		scaled[l] = scaled[l] + scaled[s] - 1 // 把 s 借走的部分从 l 扣除
		if scaled[l] < 1 {
			small = append(small, l)
		} else {
			large = append(large, l)
		}
	}
	// 剩余的桶概率视为 1(浮点残差)。
	for _, l := range large {
		t.prob[l] = 1
	}
	for _, s := range small {
		t.prob[s] = 1
	}
}

func (t *Table[T]) f64() float64 {
	if t.rng != nil {
		return t.rng.Float64()
	}
	return rand.Float64()
}

func (t *Table[T]) intn(n int) int {
	if t.rng != nil {
		return t.rng.IntN(n)
	}
	return rand.IntN(n)
}

// drawIndex 用 Alias Method 抽一个桶下标(O(1))。
func (t *Table[T]) drawIndex() int {
	n := len(t.items)
	col := t.intn(n)
	if t.f64() < t.prob[col] {
		return col
	}
	return t.alias[col]
}

// Draw 按权重抽取一个物品(O(1))。表非空,必返回有效值。
func (t *Table[T]) Draw() T {
	return t.items[t.drawIndex()].Value
}

// DrawItem 同 Draw,但返回完整 Item(含权重/稀有度)。
func (t *Table[T]) DrawItem() Item[T] {
	return t.items[t.drawIndex()]
}

// DrawN 抽取 n 个(放回:每次独立按权重抽,可能重复)。用于普通连抽。
func (t *Table[T]) DrawN(n int) []T {
	if n <= 0 {
		return nil
	}
	out := make([]T, n)
	for i := range out {
		out[i] = t.Draw()
	}
	return out
}

// DrawDistinct 不放回抽取 n 个互不相同的物品(按权重加权,已抽中的不再重复)。
// n 超过表内物品数时,返回全部物品(数量 < n)。用于"十连里不重复"的场景。
// 复杂度 O(k·m)(k=抽取数,m=剩余物品数),不走 Alias(动态权重),适合 n 较小的连抽。
func (t *Table[T]) DrawDistinct(n int) []T {
	if n <= 0 {
		return nil
	}
	m := len(t.items)
	if n > m {
		n = m
	}
	// 复制权重,抽中即置 0。
	remaining := make([]float64, m)
	total := 0.0
	for i, it := range t.items {
		remaining[i] = it.Weight
		total += it.Weight
	}
	out := make([]T, 0, n)
	for range n {
		r := t.f64() * total
		var pick int = -1
		acc := 0.0
		for i := range m {
			if remaining[i] <= 0 {
				continue
			}
			acc += remaining[i]
			if r < acc {
				pick = i
				break
			}
		}
		if pick < 0 {
			// 浮点残差兜底:取最后一个仍有权重的。
			for i := m - 1; i >= 0; i-- {
				if remaining[i] > 0 {
					pick = i
					break
				}
			}
		}
		if pick < 0 {
			break
		}
		out = append(out, t.items[pick].Value)
		total -= remaining[pick]
		remaining[pick] = 0
	}
	return out
}

// Len 返回表内有效物品数。
func (t *Table[T]) Len() int { return len(t.items) }

// TotalWeight 返回所有有效物品的权重之和。
func (t *Table[T]) TotalWeight() float64 { return t.totalW }
