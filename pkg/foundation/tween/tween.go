// Package tween 提供缓动函数与贝塞尔曲线插值原语,用于平滑动画和轨迹模拟。
//
// 适用:UI 动画(弹出/淡入)、赛车漂移轨迹、RPG 飞弹抛物线、镜头追踪、
// 状态同步中的客户端预测平滑。
//
// 两大组件:
//   - Ease 函数:输入 t∈[0,1],输出 [0,1] 的变换后进度。提供业界标准的
//     EaseIn/EaseOut/EaseInOut 系列(Quad、Cubic、Quart、Quint、Sine、Expo、
//     Circ、Back、Elastic、Bounce)。
//   - 贝塞尔曲线:二次(Quadratic)与三次(Cubic)Bézier,支持求值、
//     弧长近似、等速采样。
//
// 值类型,无状态,并发安全。
package tween

import "math"

// ---------- 缓动函数 ----------

// EaseFunc 缓动函数签名:t∈[0,1] → [0,1]。
type EaseFunc func(t float64) float64

// clamp01 限制 t 到 [0,1]。
func clamp01(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t
}

// Linear 线性(无缓动)。
func Linear(t float64) float64 { return clamp01(t) }

// --- Quad ---

func EaseInQuad(t float64) float64  { t = clamp01(t); return t * t }
func EaseOutQuad(t float64) float64 { t = clamp01(t); return t * (2 - t) }
func EaseInOutQuad(t float64) float64 {
	t = clamp01(t)
	t *= 2
	if t < 1 {
		return 0.5 * t * t
	}
	t--
	return -0.5 * (t*(t-2) - 1)
}

// --- Cubic ---

func EaseInCubic(t float64) float64  { t = clamp01(t); return t * t * t }
func EaseOutCubic(t float64) float64 { t = clamp01(t); t--; return t*t*t + 1 }
func EaseInOutCubic(t float64) float64 {
	t = clamp01(t)
	t *= 2
	if t < 1 {
		return 0.5 * t * t * t
	}
	t -= 2
	return 0.5 * (t*t*t + 2)
}

// --- Quart ---

func EaseInQuart(t float64) float64  { t = clamp01(t); return t * t * t * t }
func EaseOutQuart(t float64) float64 { t = clamp01(t); t--; return -(t*t*t*t - 1) }
func EaseInOutQuart(t float64) float64 {
	t = clamp01(t)
	t *= 2
	if t < 1 {
		return 0.5 * t * t * t * t
	}
	t -= 2
	return -0.5 * (t*t*t*t - 2)
}

// --- Quint ---

func EaseInQuint(t float64) float64  { t = clamp01(t); return t * t * t * t * t }
func EaseOutQuint(t float64) float64 { t = clamp01(t); t--; return t*t*t*t*t + 1 }
func EaseInOutQuint(t float64) float64 {
	t = clamp01(t)
	t *= 2
	if t < 1 {
		return 0.5 * t * t * t * t * t
	}
	t -= 2
	return 0.5 * (t*t*t*t*t + 2)
}

// --- Sine ---

func EaseInSine(t float64) float64    { t = clamp01(t); return 1 - math.Cos(t*math.Pi/2) }
func EaseOutSine(t float64) float64   { t = clamp01(t); return math.Sin(t * math.Pi / 2) }
func EaseInOutSine(t float64) float64 { t = clamp01(t); return -0.5 * (math.Cos(math.Pi*t) - 1) }

// --- Expo ---

func EaseInExpo(t float64) float64 {
	t = clamp01(t)
	if t == 0 {
		return 0
	}
	return math.Pow(2, 10*(t-1))
}

func EaseOutExpo(t float64) float64 {
	t = clamp01(t)
	if t == 1 {
		return 1
	}
	return 1 - math.Pow(2, -10*t)
}

func EaseInOutExpo(t float64) float64 {
	t = clamp01(t)
	if t == 0 {
		return 0
	}
	if t == 1 {
		return 1
	}
	t *= 2
	if t < 1 {
		return 0.5 * math.Pow(2, 10*(t-1))
	}
	return 0.5 * (2 - math.Pow(2, -10*(t-1)))
}

// --- Circ ---

func EaseInCirc(t float64) float64  { t = clamp01(t); return 1 - math.Sqrt(1-t*t) }
func EaseOutCirc(t float64) float64 { t = clamp01(t); t--; return math.Sqrt(1 - t*t) }
func EaseInOutCirc(t float64) float64 {
	t = clamp01(t)
	t *= 2
	if t < 1 {
		return -0.5 * (math.Sqrt(1-t*t) - 1)
	}
	t -= 2
	return 0.5 * (math.Sqrt(1-t*t) + 1)
}

// --- Back ---

const backS = 1.70158

func EaseInBack(t float64) float64  { t = clamp01(t); return t * t * ((backS+1)*t - backS) }
func EaseOutBack(t float64) float64 { t = clamp01(t); t--; return t*t*((backS+1)*t+backS) + 1 }
func EaseInOutBack(t float64) float64 {
	t = clamp01(t)
	s := backS * 1.525
	t *= 2
	if t < 1 {
		return 0.5 * (t * t * ((s+1)*t - s))
	}
	t -= 2
	return 0.5 * (t*t*((s+1)*t+s) + 2)
}

// --- Elastic ---

func EaseInElastic(t float64) float64 {
	t = clamp01(t)
	if t == 0 || t == 1 {
		return t
	}
	return -math.Pow(2, 10*(t-1)) * math.Sin((t-1.1)*5*math.Pi)
}

func EaseOutElastic(t float64) float64 {
	t = clamp01(t)
	if t == 0 || t == 1 {
		return t
	}
	return math.Pow(2, -10*t)*math.Sin((t-0.1)*5*math.Pi) + 1
}

func EaseInOutElastic(t float64) float64 {
	t = clamp01(t)
	if t == 0 || t == 1 {
		return t
	}
	t *= 2
	if t < 1 {
		return -0.5 * math.Pow(2, 10*(t-1)) * math.Sin((t-1.1)*5*math.Pi)
	}
	return 0.5*math.Pow(2, -10*(t-1))*math.Sin((t-1.1)*5*math.Pi) + 1
}

// --- Bounce ---

func EaseOutBounce(t float64) float64 {
	t = clamp01(t)
	switch {
	case t < 1.0/2.75:
		return 7.5625 * t * t
	case t < 2.0/2.75:
		t -= 1.5 / 2.75
		return 7.5625*t*t + 0.75
	case t < 2.5/2.75:
		t -= 2.25 / 2.75
		return 7.5625*t*t + 0.9375
	default:
		t -= 2.625 / 2.75
		return 7.5625*t*t + 0.984375
	}
}

func EaseInBounce(t float64) float64 { return 1 - EaseOutBounce(1-clamp01(t)) }
func EaseInOutBounce(t float64) float64 {
	t = clamp01(t)
	if t < 0.5 {
		return EaseInBounce(2*t) * 0.5
	}
	return EaseOutBounce(2*t-1)*0.5 + 0.5
}

// ---------- 贝塞尔曲线 ----------

// Vec2 二维浮点向量(轻量,仅用于曲线计算)。
type Vec2 struct {
	X, Y float64
}

// lerp2 二维线性插值。
func lerp2(a, b Vec2, t float64) Vec2 {
	return Vec2{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
	}
}

// QuadraticBezier 二次贝塞尔曲线:P0 为起点,P1 为控制点,P2 为终点。
// B(t) = (1-t)²P0 + 2(1-t)tP1 + t²P2
type QuadraticBezier struct {
	P0, P1, P2 Vec2
}

// At 求值:t∈[0,1] 返回曲线上的点。
func (b QuadraticBezier) At(t float64) Vec2 {
	t = clamp01(t)
	u := 1 - t
	return Vec2{
		X: u*u*b.P0.X + 2*u*t*b.P1.X + t*t*b.P2.X,
		Y: u*u*b.P0.Y + 2*u*t*b.P1.Y + t*t*b.P2.Y,
	}
}

// Tangent 返回参数 t 处的切线方向(未归一化)。
func (b QuadraticBezier) Tangent(t float64) Vec2 {
	t = clamp01(t)
	u := 1 - t
	return Vec2{
		X: 2*u*(b.P1.X-b.P0.X) + 2*t*(b.P2.X-b.P1.X),
		Y: 2*u*(b.P1.Y-b.P0.Y) + 2*t*(b.P2.Y-b.P1.Y),
	}
}

// CubicBezier 三次贝塞尔曲线:P0 起点,P1/P2 控制点,P3 终点。
// B(t) = (1-t)³P0 + 3(1-t)²tP1 + 3(1-t)t²P2 + t³P3
type CubicBezier struct {
	P0, P1, P2, P3 Vec2
}

// At 求值:t∈[0,1] 返回曲线上的点。
func (b CubicBezier) At(t float64) Vec2 {
	t = clamp01(t)
	u := 1 - t
	uu := u * u
	tt := t * t
	return Vec2{
		X: uu*u*b.P0.X + 3*uu*t*b.P1.X + 3*u*tt*b.P2.X + tt*t*b.P3.X,
		Y: uu*u*b.P0.Y + 3*uu*t*b.P1.Y + 3*u*tt*b.P2.Y + tt*t*b.P3.Y,
	}
}

// Tangent 返回参数 t 处的切线方向(未归一化)。
func (b CubicBezier) Tangent(t float64) Vec2 {
	t = clamp01(t)
	u := 1 - t
	return Vec2{
		X: 3*u*u*(b.P1.X-b.P0.X) + 6*u*t*(b.P2.X-b.P1.X) + 3*t*t*(b.P3.X-b.P2.X),
		Y: 3*u*u*(b.P1.Y-b.P0.Y) + 6*u*t*(b.P2.Y-b.P1.Y) + 3*t*t*(b.P3.Y-b.P2.Y),
	}
}

// ArcLength 用 N 段线性近似估计曲线弧长。segments <= 0 时默认 64。
func (b CubicBezier) ArcLength(segments int) float64 {
	if segments <= 0 {
		segments = 64
	}
	length := 0.0
	prev := b.P0
	for i := 1; i <= segments; i++ {
		t := float64(i) / float64(segments)
		cur := b.At(t)
		dx, dy := cur.X-prev.X, cur.Y-prev.Y
		length += math.Sqrt(dx*dx + dy*dy)
		prev = cur
	}
	return length
}

// EvenSample 等弧长采样:返回 n+1 个均匀分布在曲线上的点(含首尾)。
// 用于匀速运动(避免原始 t 参数化带来的变速感)。n <= 0 时返回首尾两点。
func (b CubicBezier) EvenSample(n int) []Vec2 {
	if n <= 0 {
		return []Vec2{b.P0, b.P3}
	}
	const lutSize = 256
	lut := make([]float64, lutSize+1)
	prev := b.P0
	for i := 1; i <= lutSize; i++ {
		t := float64(i) / float64(lutSize)
		cur := b.At(t)
		dx, dy := cur.X-prev.X, cur.Y-prev.Y
		lut[i] = lut[i-1] + math.Sqrt(dx*dx+dy*dy)
		prev = cur
	}
	totalLen := lut[lutSize]
	if totalLen == 0 {
		pts := make([]Vec2, n+1)
		for i := range pts {
			pts[i] = b.P0
		}
		return pts
	}

	pts := make([]Vec2, n+1)
	pts[0] = b.P0
	pts[n] = b.P3
	for i := 1; i < n; i++ {
		target := totalLen * float64(i) / float64(n)
		// 二分查找对应 t
		lo, hi := 0, lutSize
		for lo < hi {
			mid := (lo + hi) / 2
			if lut[mid] < target {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		seg := lo
		if seg == 0 {
			pts[i] = b.P0
			continue
		}
		segLen := lut[seg] - lut[seg-1]
		frac := 0.0
		if segLen > 0 {
			frac = (target - lut[seg-1]) / segLen
		}
		t := (float64(seg-1) + frac) / float64(lutSize)
		pts[i] = b.At(t)
	}
	return pts
}
