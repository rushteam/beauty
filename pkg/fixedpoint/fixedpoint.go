// Package fixedpoint 提供 Q32.32 定点数原语,用于帧同步/状态同步场景下的跨平台
// 确定性数学计算。
//
// 解决的问题:帧同步要求"相同输入在任何机器上计算结果完全一致",但不同 CPU 的
// 浮点运算(IEEE 754 舍入、FMA 指令、x87 80 位寄存器)可能产生 1 ULP 级别的差异,
// 积累后导致不同步(desync)。定点数用整数运算模拟小数,彻底消除浮点不确定性。
//
// 格式:Q32.32 — 用 int64 表示,高 32 位为整数部分,低 32 位为小数部分。
// 精度约 2.3e-10(远超游戏物理需求),整数范围 ±2^31 ≈ ±21 亿。
//
// 与相邻原语的关系:
//   - gameloop/match 的 OnTick 内使用 fixedpoint 做物理计算,保证确定性;
//   - snapbuf 存的快照中坐标/速度用 fixedpoint.Fixed 而非 float64;
//   - loot 如需确定性抽取,可将权重转为 Fixed 做累加比较。
//
// 并发安全:Fixed 是值类型(int64),无共享状态。Vec2 亦为值类型。
package fixedpoint

import "fmt"

// shift 是小数部分的位数(Q32.32)。
const shift = 32

// one 是定点数 1.0 的内部表示。
const one int64 = 1 << shift

// Fixed 是 Q32.32 定点数,用 int64 的低 32 位表示小数、高 32 位表示整数。
// 零值即 0。
type Fixed int64

// ---------- 构造 ----------

// FromInt 把整数转为定点数。
func FromInt(n int) Fixed { return Fixed(int64(n) << shift) }

// FromInt64 把 int64 转为定点数。
func FromInt64(n int64) Fixed { return Fixed(n << shift) }

// FromFloat64 把浮点数转为定点数(仅用于初始化常量/配置,运行时应避免)。
func FromFloat64(f float64) Fixed { return Fixed(int64(f * float64(one))) }

// FromFrac 用分子/分母构造定点数(纯整数运算,无浮点)。denom 为 0 时 panic。
func FromFrac(num, denom int64) Fixed {
	if denom == 0 {
		panic("fixedpoint: division by zero in FromFrac")
	}
	return Fixed((num << shift) / denom)
}

// Raw 直接用原始 int64 值构造(高级用途:反序列化/网络传输)。
func Raw(v int64) Fixed { return Fixed(v) }

// ---------- 转换 ----------

// Int 截断为整数(向零取整)。
func (a Fixed) Int() int { return int(int64(a) >> shift) }

// Int64 截断为 int64(向零取整)。
func (a Fixed) Int64() int64 { return int64(a) >> shift }

// Float64 转为浮点(仅用于调试/日志,勿用于确定性计算)。
func (a Fixed) Float64() float64 { return float64(a) / float64(one) }

// Raw 返回底层 int64(序列化/网络传输)。
func (a Fixed) Raw() int64 { return int64(a) }

func (a Fixed) String() string { return fmt.Sprintf("%.6f", a.Float64()) }

// ---------- 算术 ----------

// Add 加法。
func (a Fixed) Add(b Fixed) Fixed { return a + b }

// Sub 减法。
func (a Fixed) Sub(b Fixed) Fixed { return a - b }

// Mul 乘法(128 位中间结果防溢出)。
func (a Fixed) Mul(b Fixed) Fixed {
	return Fixed(mul64(int64(a), int64(b)))
}

// Div 除法。b 为 0 时 panic。
func (a Fixed) Div(b Fixed) Fixed {
	if b == 0 {
		panic("fixedpoint: division by zero")
	}
	return Fixed(div64(int64(a), int64(b)))
}

// Neg 取反。
func (a Fixed) Neg() Fixed { return -a }

// Abs 绝对值。
func (a Fixed) Abs() Fixed {
	if a < 0 {
		return -a
	}
	return a
}

// Floor 向下取整到最近的整数定点数。
func (a Fixed) Floor() Fixed {
	return Fixed(int64(a) &^ (one - 1))
}

// Ceil 向上取整到最近的整数定点数。
func (a Fixed) Ceil() Fixed {
	return Fixed((int64(a) + one - 1) &^ (one - 1))
}

// Round 四舍五入到最近的整数定点数。
func (a Fixed) Round() Fixed {
	return Fixed((int64(a) + one/2) &^ (one - 1))
}

// ---------- 比较 ----------

// Min 返回较小值。
func Min(a, b Fixed) Fixed {
	if a < b {
		return a
	}
	return b
}

// Max 返回较大值。
func Max(a, b Fixed) Fixed {
	if a > b {
		return a
	}
	return b
}

// Clamp 限制到 [lo, hi] 区间。
func Clamp(v, lo, hi Fixed) Fixed {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Sign 返回符号:-1、0 或 1(定点数)。
func (a Fixed) Sign() Fixed {
	switch {
	case a > 0:
		return Fixed(one)
	case a < 0:
		return Fixed(-one)
	default:
		return 0
	}
}

// Lerp 线性插值:a + (b-a)*t,t 为 [0,1] 的定点数。
func Lerp(a, b, t Fixed) Fixed {
	return a.Add(b.Sub(a).Mul(t))
}

// ---------- 128 位算术(防溢出) ----------

// mul64 执行 (a * b) >> shift,用 128 位中间结果。
func mul64(a, b int64) int64 {
	// 分解为 unsigned 高低 32 位乘法,最后修正符号。
	neg := false
	if a < 0 {
		a = -a
		neg = !neg
	}
	if b < 0 {
		b = -b
		neg = !neg
	}

	aHi := uint64(a) >> 32
	aLo := uint64(a) & 0xFFFFFFFF
	bHi := uint64(b) >> 32
	bLo := uint64(b) & 0xFFFFFFFF

	// 128 位乘法拆为 4 个 64 位乘:
	// result = (aHi*bHi)<<64 + (aHi*bLo + aLo*bHi)<<32 + aLo*bLo
	// 最终右移 shift(32) 位取 Q32.32 结果。
	mid := aHi*bLo + aLo*bHi
	lo := aLo * bLo

	// 右移 32 位:低位部分的高 32 位 + mid 的低 32 位 + aHi*bHi 的贡献。
	result := (lo >> 32) + mid + (aHi*bHi)<<32

	if neg {
		return -int64(result)
	}
	return int64(result)
}

// div64 执行 (a << shift) / b,用 128 位中间结果。
func div64(a, b int64) int64 {
	neg := false
	ua := uint64(a)
	ub := uint64(b)
	if a < 0 {
		ua = uint64(-a)
		neg = !neg
	}
	if b < 0 {
		ub = uint64(-b)
		neg = !neg
	}

	// numerator = ua << 32,拆为 (hi, lo)。
	numHi := ua >> 32
	numLo := ua << 32

	// 简单的 128÷64 除法:
	// (numHi:numLo) / ub
	qHi := numHi / ub
	rem := numHi % ub
	remFull := (rem << 32) | (numLo & 0xFFFFFFFF)
	qLo := remFull / ub

	result := (qHi << 32) | qLo
	if neg {
		return -int64(result)
	}
	return int64(result)
}

// ---------- Vec2 二维向量 ----------

// Vec2 是二维定点数向量(值类型)。
type Vec2 struct {
	X, Y Fixed
}

// V2 构造 Vec2。
func V2(x, y Fixed) Vec2 { return Vec2{X: x, Y: y} }

// Add 向量加法。
func (v Vec2) Add(o Vec2) Vec2 { return Vec2{X: v.X + o.X, Y: v.Y + o.Y} }

// Sub 向量减法。
func (v Vec2) Sub(o Vec2) Vec2 { return Vec2{X: v.X - o.X, Y: v.Y - o.Y} }

// Scale 标量乘法。
func (v Vec2) Scale(s Fixed) Vec2 { return Vec2{X: v.X.Mul(s), Y: v.Y.Mul(s)} }

// Dot 点积。
func (v Vec2) Dot(o Vec2) Fixed { return v.X.Mul(o.X).Add(v.Y.Mul(o.Y)) }

// Cross 叉积(二维叉积为标量)。
func (v Vec2) Cross(o Vec2) Fixed { return v.X.Mul(o.Y).Sub(v.Y.Mul(o.X)) }

// LenSq 长度的平方(避免开方,常用于距离比较)。
func (v Vec2) LenSq() Fixed { return v.Dot(v) }

// Len 向量长度(定点数牛顿迭代法求平方根)。
func (v Vec2) Len() Fixed { return Sqrt(v.LenSq()) }

// Normalize 归一化为单位向量。零向量返回零向量。
func (v Vec2) Normalize() Vec2 {
	l := v.Len()
	if l == 0 {
		return Vec2{}
	}
	return Vec2{X: v.X.Div(l), Y: v.Y.Div(l)}
}

// DistSq 两点距离的平方。
func DistSq(a, b Vec2) Fixed { return a.Sub(b).LenSq() }

// Dist 两点距离。
func Dist(a, b Vec2) Fixed { return Sqrt(DistSq(a, b)) }

func (v Vec2) String() string {
	return fmt.Sprintf("(%s, %s)", v.X, v.Y)
}

// ---------- 数学函数 ----------

// Sqrt 定点数平方根(牛顿迭代法,纯整数运算)。负数 panic。
func Sqrt(a Fixed) Fixed {
	if a < 0 {
		panic("fixedpoint: sqrt of negative number")
	}
	if a == 0 {
		return 0
	}

	// 牛顿迭代:x_{n+1} = (x_n + a/x_n) / 2
	// 在 raw int64 域操作以保持精度。
	n := int64(a)

	// 初始猜测:取 n 的位宽一半对应的值。
	x := n
	if x > one {
		x = one
	}
	if x == 0 {
		x = 1
	}

	for range 64 {
		// x1 = (x + (a<<shift)/x) >> 1
		// 用 a<<shift 是因为 Sqrt(a) 在 Q32.32 里 = sqrt(raw_a) << 16
		nx := (x + (n<<shift)/x) >> 1
		if nx >= x {
			break
		}
		x = nx
	}
	return Fixed(x)
}
