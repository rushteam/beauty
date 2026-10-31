package sketch

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func exactQuantile(xs []float64, q float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if q <= 0 {
		return s[0]
	}
	if q >= 1 {
		return s[len(s)-1]
	}
	return s[int(q*float64(len(s)))]
}

func TestTDigestUniformAccuracy(t *testing.T) {
	td := NewTDigest(100)
	rng := rand.New(rand.NewSource(1))
	xs := make([]float64, 100000)
	for i := range xs {
		xs[i] = rng.Float64() * 1000
		td.Add(xs[i])
	}
	if td.Count() != 100000 {
		t.Fatalf("count=%v", td.Count())
	}
	for _, q := range []float64{0.01, 0.1, 0.5, 0.9, 0.99, 0.999} {
		got := td.Quantile(q)
		want := exactQuantile(xs, q)
		// 均匀分布 [0,1000],容许绝对误差 ~1%(10)。
		if math.Abs(got-want) > 12 {
			t.Errorf("q=%.3f got=%.2f want=%.2f (err=%.2f)", q, got, want, math.Abs(got-want))
		}
	}
}

func TestTDigestMonotonic(t *testing.T) {
	td := NewTDigest(50)
	for i := 0; i < 10000; i++ {
		td.Add(float64(i))
	}
	prev := math.Inf(-1)
	for q := 0.0; q <= 1.0; q += 0.05 {
		v := td.Quantile(q)
		if v < prev {
			t.Fatalf("not monotonic at q=%.2f: %.2f < %.2f", q, v, prev)
		}
		prev = v
	}
	if td.Min() != 0 || td.Max() != 9999 {
		t.Fatalf("min/max = %v/%v", td.Min(), td.Max())
	}
}

func TestTDigestMerge(t *testing.T) {
	a := NewTDigest(100)
	b := NewTDigest(100)
	all := make([]float64, 0, 40000)
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 20000; i++ {
		x := rng.NormFloat64()*10 + 100
		a.Add(x)
		all = append(all, x)
		y := rng.NormFloat64()*10 + 100
		b.Add(y)
		all = append(all, y)
	}
	a.Merge(b)
	if a.Count() != 40000 {
		t.Fatalf("merged count=%v", a.Count())
	}
	got := a.Quantile(0.5)
	want := exactQuantile(all, 0.5)
	if math.Abs(got-want) > 2 {
		t.Fatalf("merged p50 got=%.2f want=%.2f", got, want)
	}
}

func TestTDigestEmpty(t *testing.T) {
	td := NewTDigest(0) // 默认 100
	if !math.IsNaN(td.Quantile(0.5)) {
		t.Fatal("empty should be NaN")
	}
	td.Add(5)
	if td.Quantile(0.5) != 5 {
		t.Fatal("single value")
	}
}
