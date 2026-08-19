package fixedpoint_test

import (
	"math"
	"testing"

	"github.com/rushteam/beauty/pkg/fixedpoint"
)

func TestFromIntRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, -1, 42, -999, 1<<20, -(1 << 20)} {
		f := fixedpoint.FromInt(n)
		if got := f.Int(); got != n {
			t.Fatalf("FromInt(%d).Int() = %d", n, got)
		}
	}
}

func TestFromFloat64Approx(t *testing.T) {
	cases := []float64{0, 1, -1, 0.5, 0.25, 3.14159, -2.71828, 100.001}
	for _, v := range cases {
		f := fixedpoint.FromFloat64(v)
		got := f.Float64()
		if diff := math.Abs(got - v); diff > 1e-6 {
			t.Fatalf("FromFloat64(%v).Float64() = %v (diff=%v)", v, got, diff)
		}
	}
}

func TestFromFrac(t *testing.T) {
	f := fixedpoint.FromFrac(1, 3)
	if diff := math.Abs(f.Float64() - 1.0/3.0); diff > 1e-6 {
		t.Fatalf("FromFrac(1,3) = %v", f.Float64())
	}
}

func TestAdd(t *testing.T) {
	a := fixedpoint.FromFloat64(1.5)
	b := fixedpoint.FromFloat64(2.25)
	got := a.Add(b).Float64()
	if diff := math.Abs(got - 3.75); diff > 1e-9 {
		t.Fatalf("1.5+2.25 = %v", got)
	}
}

func TestSub(t *testing.T) {
	a := fixedpoint.FromFloat64(5.0)
	b := fixedpoint.FromFloat64(3.5)
	got := a.Sub(b).Float64()
	if diff := math.Abs(got - 1.5); diff > 1e-9 {
		t.Fatalf("5.0-3.5 = %v", got)
	}
}

func TestMul(t *testing.T) {
	cases := [][3]float64{
		{2, 3, 6},
		{1.5, 2, 3},
		{-3, 4, -12},
		{0.1, 0.1, 0.01},
		{100, 100, 10000},
	}
	for _, c := range cases {
		a := fixedpoint.FromFloat64(c[0])
		b := fixedpoint.FromFloat64(c[1])
		got := a.Mul(b).Float64()
		if diff := math.Abs(got - c[2]); diff > 1e-4 {
			t.Fatalf("%v*%v = %v, want %v", c[0], c[1], got, c[2])
		}
	}
}

func TestDiv(t *testing.T) {
	a := fixedpoint.FromFloat64(10.0)
	b := fixedpoint.FromFloat64(3.0)
	got := a.Div(b).Float64()
	if diff := math.Abs(got - 10.0/3.0); diff > 1e-4 {
		t.Fatalf("10/3 = %v", got)
	}
}

func TestDivByZeroPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on division by zero")
		}
	}()
	fixedpoint.FromInt(1).Div(0)
}

func TestNeg(t *testing.T) {
	a := fixedpoint.FromFloat64(3.5)
	if got := a.Neg().Float64(); math.Abs(got+3.5) > 1e-9 {
		t.Fatalf("Neg(3.5) = %v", got)
	}
}

func TestAbs(t *testing.T) {
	a := fixedpoint.FromFloat64(-7.0)
	if got := a.Abs().Float64(); math.Abs(got-7.0) > 1e-9 {
		t.Fatalf("Abs(-7) = %v", got)
	}
}

func TestFloorCeilRound(t *testing.T) {
	a := fixedpoint.FromFloat64(2.7)
	if f := a.Floor().Int(); f != 2 {
		t.Fatalf("Floor(2.7) = %d", f)
	}
	if c := a.Ceil().Int(); c != 3 {
		t.Fatalf("Ceil(2.7) = %d", c)
	}
	if r := a.Round().Int(); r != 3 {
		t.Fatalf("Round(2.7) = %d", r)
	}
	b := fixedpoint.FromFloat64(2.3)
	if r := b.Round().Int(); r != 2 {
		t.Fatalf("Round(2.3) = %d", r)
	}
}

func TestMinMaxClamp(t *testing.T) {
	a := fixedpoint.FromInt(3)
	b := fixedpoint.FromInt(7)
	if fixedpoint.Min(a, b) != a {
		t.Fatal("Min failed")
	}
	if fixedpoint.Max(a, b) != b {
		t.Fatal("Max failed")
	}
	lo := fixedpoint.FromInt(2)
	hi := fixedpoint.FromInt(5)
	if fixedpoint.Clamp(b, lo, hi) != hi {
		t.Fatal("Clamp upper failed")
	}
	if fixedpoint.Clamp(fixedpoint.FromInt(1), lo, hi) != lo {
		t.Fatal("Clamp lower failed")
	}
}

func TestLerp(t *testing.T) {
	a := fixedpoint.FromFloat64(0)
	b := fixedpoint.FromFloat64(10)
	half := fixedpoint.FromFloat64(0.5)
	got := fixedpoint.Lerp(a, b, half).Float64()
	if diff := math.Abs(got - 5.0); diff > 1e-6 {
		t.Fatalf("Lerp(0,10,0.5) = %v", got)
	}
}

func TestSqrt(t *testing.T) {
	cases := []float64{0, 1, 4, 9, 2, 0.25, 100, 10000}
	for _, v := range cases {
		got := fixedpoint.Sqrt(fixedpoint.FromFloat64(v)).Float64()
		want := math.Sqrt(v)
		if diff := math.Abs(got - want); diff > 0.01 {
			t.Fatalf("Sqrt(%v) = %v, want %v", v, got, want)
		}
	}
}

func TestVec2AddSub(t *testing.T) {
	a := fixedpoint.V2(fixedpoint.FromInt(1), fixedpoint.FromInt(2))
	b := fixedpoint.V2(fixedpoint.FromInt(3), fixedpoint.FromInt(4))
	sum := a.Add(b)
	if sum.X.Int() != 4 || sum.Y.Int() != 6 {
		t.Fatalf("Add = %v", sum)
	}
	diff := b.Sub(a)
	if diff.X.Int() != 2 || diff.Y.Int() != 2 {
		t.Fatalf("Sub = %v", diff)
	}
}

func TestVec2Dot(t *testing.T) {
	a := fixedpoint.V2(fixedpoint.FromInt(3), fixedpoint.FromInt(4))
	b := fixedpoint.V2(fixedpoint.FromInt(1), fixedpoint.FromInt(2))
	// 3*1 + 4*2 = 11
	if got := a.Dot(b).Int(); got != 11 {
		t.Fatalf("Dot = %d", got)
	}
}

func TestVec2Len(t *testing.T) {
	v := fixedpoint.V2(fixedpoint.FromInt(3), fixedpoint.FromInt(4))
	got := v.Len().Float64()
	if diff := math.Abs(got - 5.0); diff > 0.01 {
		t.Fatalf("Len(3,4) = %v", got)
	}
}

func TestVec2Normalize(t *testing.T) {
	v := fixedpoint.V2(fixedpoint.FromInt(3), fixedpoint.FromInt(4))
	n := v.Normalize()
	lenN := n.Len().Float64()
	if diff := math.Abs(lenN - 1.0); diff > 0.01 {
		t.Fatalf("Normalized length = %v", lenN)
	}
}

func TestDist(t *testing.T) {
	a := fixedpoint.V2(fixedpoint.FromInt(0), fixedpoint.FromInt(0))
	b := fixedpoint.V2(fixedpoint.FromInt(3), fixedpoint.FromInt(4))
	got := fixedpoint.Dist(a, b).Float64()
	if diff := math.Abs(got - 5.0); diff > 0.01 {
		t.Fatalf("Dist = %v", got)
	}
}

func TestRawRoundTrip(t *testing.T) {
	orig := fixedpoint.FromFloat64(3.14)
	raw := orig.Raw()
	back := fixedpoint.Raw(raw)
	if orig != back {
		t.Fatalf("Raw round-trip: %v != %v", orig, back)
	}
}

func TestSign(t *testing.T) {
	pos := fixedpoint.FromInt(5)
	neg := fixedpoint.FromInt(-3)
	zero := fixedpoint.FromInt(0)
	if pos.Sign().Int() != 1 {
		t.Fatal("Sign(pos)")
	}
	if neg.Sign().Int() != -1 {
		t.Fatal("Sign(neg)")
	}
	if zero.Sign().Int() != 0 {
		t.Fatal("Sign(zero)")
	}
}

func TestDeterminism(t *testing.T) {
	// 同样的输入必须产生完全一致的 Raw 值——跨平台确定性的核心承诺。
	a := fixedpoint.FromFrac(355, 113) // ≈ π
	b := fixedpoint.FromFrac(1, 7)
	result := a.Mul(b).Add(a.Div(b)).Sub(fixedpoint.FromInt(1))
	// 在任何机器上这个 Raw 值必须相同。
	const expected int64 = 90451055259 // 预先计算
	if result.Raw() != expected {
		t.Fatalf("determinism check: raw=%d, want %d", result.Raw(), expected)
	}
}

func BenchmarkMul(b *testing.B) {
	a := fixedpoint.FromFloat64(3.14159)
	c := fixedpoint.FromFloat64(2.71828)
	for range b.N {
		a.Mul(c)
	}
}

func BenchmarkDiv(b *testing.B) {
	a := fixedpoint.FromFloat64(3.14159)
	c := fixedpoint.FromFloat64(2.71828)
	for range b.N {
		a.Div(c)
	}
}

func BenchmarkSqrt(b *testing.B) {
	a := fixedpoint.FromFloat64(12345.6789)
	for range b.N {
		fixedpoint.Sqrt(a)
	}
}
