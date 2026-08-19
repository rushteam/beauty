package tween

import (
	"math"
	"testing"
)

func TestLinear(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 0}, {0.5, 0.5}, {1, 1}, {-1, 0}, {2, 1},
	}
	for _, c := range cases {
		if got := Linear(c.in); got != c.want {
			t.Errorf("Linear(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestEaseBoundaries(t *testing.T) {
	fns := []struct {
		name string
		fn   EaseFunc
	}{
		{"EaseInQuad", EaseInQuad},
		{"EaseOutQuad", EaseOutQuad},
		{"EaseInOutQuad", EaseInOutQuad},
		{"EaseInCubic", EaseInCubic},
		{"EaseOutCubic", EaseOutCubic},
		{"EaseInOutCubic", EaseInOutCubic},
		{"EaseInSine", EaseInSine},
		{"EaseOutSine", EaseOutSine},
		{"EaseInOutSine", EaseInOutSine},
		{"EaseInExpo", EaseInExpo},
		{"EaseOutExpo", EaseOutExpo},
		{"EaseInOutExpo", EaseInOutExpo},
		{"EaseInCirc", EaseInCirc},
		{"EaseOutCirc", EaseOutCirc},
		{"EaseInOutCirc", EaseInOutCirc},
		{"EaseInBack", EaseInBack},
		{"EaseOutBack", EaseOutBack},
		{"EaseInOutBack", EaseInOutBack},
		{"EaseInElastic", EaseInElastic},
		{"EaseOutElastic", EaseOutElastic},
		{"EaseInOutElastic", EaseInOutElastic},
		{"EaseInBounce", EaseInBounce},
		{"EaseOutBounce", EaseOutBounce},
		{"EaseInOutBounce", EaseInOutBounce},
		{"EaseInQuart", EaseInQuart},
		{"EaseOutQuart", EaseOutQuart},
		{"EaseInOutQuart", EaseInOutQuart},
		{"EaseInQuint", EaseInQuint},
		{"EaseOutQuint", EaseOutQuint},
		{"EaseInOutQuint", EaseInOutQuint},
	}
	const eps = 1e-9
	for _, f := range fns {
		v0 := f.fn(0)
		v1 := f.fn(1)
		if math.Abs(v0) > eps {
			t.Errorf("%s(0) = %v, want ~0", f.name, v0)
		}
		if math.Abs(v1-1) > eps {
			t.Errorf("%s(1) = %v, want ~1", f.name, v1)
		}
	}
}

func TestEaseMonotonic(t *testing.T) {
	monotonic := []EaseFunc{
		EaseInQuad, EaseOutQuad, EaseInOutQuad,
		EaseInCubic, EaseOutCubic, EaseInOutCubic,
		EaseInSine, EaseOutSine, EaseInOutSine,
	}
	for i, fn := range monotonic {
		prev := fn(0)
		for step := 1; step <= 100; step++ {
			cur := fn(float64(step) / 100)
			if cur < prev-1e-12 {
				t.Errorf("ease[%d] not monotonic at step %d: %v > %v", i, step, prev, cur)
			}
			prev = cur
		}
	}
}

func TestQuadraticBezier(t *testing.T) {
	b := QuadraticBezier{
		P0: Vec2{0, 0},
		P1: Vec2{0.5, 1},
		P2: Vec2{1, 0},
	}
	p0 := b.At(0)
	if p0.X != 0 || p0.Y != 0 {
		t.Errorf("At(0) = %v, want (0,0)", p0)
	}
	p1 := b.At(1)
	if p1.X != 1 || p1.Y != 0 {
		t.Errorf("At(1) = %v, want (1,0)", p1)
	}
	mid := b.At(0.5)
	if math.Abs(mid.X-0.5) > 1e-9 || math.Abs(mid.Y-0.5) > 1e-9 {
		t.Errorf("At(0.5) = %v, want ~(0.5, 0.5)", mid)
	}
}

func TestCubicBezier(t *testing.T) {
	b := CubicBezier{
		P0: Vec2{0, 0},
		P1: Vec2{0, 1},
		P2: Vec2{1, 1},
		P3: Vec2{1, 0},
	}
	p0 := b.At(0)
	if p0.X != 0 || p0.Y != 0 {
		t.Errorf("At(0) = %v, want (0,0)", p0)
	}
	p1 := b.At(1)
	if p1.X != 1 || p1.Y != 0 {
		t.Errorf("At(1) = %v, want (1,0)", p1)
	}
	// 对称曲线,t=0.5 应在 (0.5, 0.75)
	mid := b.At(0.5)
	if math.Abs(mid.X-0.5) > 1e-9 || math.Abs(mid.Y-0.75) > 1e-9 {
		t.Errorf("At(0.5) = %v, want ~(0.5, 0.75)", mid)
	}
}

func TestCubicBezierTangent(t *testing.T) {
	b := CubicBezier{
		P0: Vec2{0, 0},
		P1: Vec2{1, 0},
		P2: Vec2{1, 1},
		P3: Vec2{0, 1},
	}
	tan0 := b.Tangent(0)
	if math.Abs(tan0.Y) > 1e-9 {
		t.Errorf("Tangent(0).Y = %v, want ~0 (horizontal start)", tan0.Y)
	}
	if tan0.X <= 0 {
		t.Errorf("Tangent(0).X = %v, want > 0", tan0.X)
	}
}

func TestCubicBezierArcLength(t *testing.T) {
	// 直线 (0,0)→(1,0),弧长应为 1
	b := CubicBezier{
		P0: Vec2{0, 0},
		P1: Vec2{1.0 / 3, 0},
		P2: Vec2{2.0 / 3, 0},
		P3: Vec2{1, 0},
	}
	length := b.ArcLength(100)
	if math.Abs(length-1) > 1e-6 {
		t.Errorf("ArcLength of straight line = %v, want ~1", length)
	}
}

func TestCubicBezierEvenSample(t *testing.T) {
	b := CubicBezier{
		P0: Vec2{0, 0},
		P1: Vec2{1.0 / 3, 0},
		P2: Vec2{2.0 / 3, 0},
		P3: Vec2{1, 0},
	}
	pts := b.EvenSample(4)
	if len(pts) != 5 {
		t.Fatalf("EvenSample(4) returned %d points, want 5", len(pts))
	}
	for i, p := range pts {
		want := float64(i) / 4.0
		if math.Abs(p.X-want) > 1e-3 {
			t.Errorf("pts[%d].X = %v, want ~%v", i, p.X, want)
		}
	}
}

func TestQuadraticBezierTangent(t *testing.T) {
	b := QuadraticBezier{
		P0: Vec2{0, 0},
		P1: Vec2{0.5, 1},
		P2: Vec2{1, 0},
	}
	tan0 := b.Tangent(0)
	if math.Abs(tan0.X-1) > 1e-9 || math.Abs(tan0.Y-2) > 1e-9 {
		t.Errorf("Tangent(0) = %v, want (1,2)", tan0)
	}
}
