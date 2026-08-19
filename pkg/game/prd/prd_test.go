package prd

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestCFromP_Boundaries(t *testing.T) {
	if c := CFromP(0); c != 0 {
		t.Errorf("CFromP(0) = %v, want 0", c)
	}
	if c := CFromP(1); c != 1 {
		t.Errorf("CFromP(1) = %v, want 1", c)
	}
	if c := CFromP(-0.5); c != 0 {
		t.Errorf("CFromP(-0.5) = %v, want 0", c)
	}
	if c := CFromP(1.5); c != 1 {
		t.Errorf("CFromP(1.5) = %v, want 1", c)
	}
}

func TestCFromP_Monotonic(t *testing.T) {
	prev := CFromP(0.01)
	for i := 2; i <= 100; i++ {
		p := float64(i) / 100
		c := CFromP(p)
		if c < prev {
			t.Errorf("CFromP not monotonic at p=%v: %v < %v", p, c, prev)
		}
		prev = c
	}
}

func TestCFromP_CLessThanP(t *testing.T) {
	for i := 1; i <= 99; i++ {
		p := float64(i) / 100
		c := CFromP(p)
		if c > p+1e-9 {
			t.Errorf("CFromP(%v) = %v > P (should be C <= P)", p, c)
		}
	}
}

func TestRoller_EventuallyTriggers(t *testing.T) {
	r := New(0.25)
	for i := range 1000 {
		if r.Roll() {
			return
		}
		_ = i
	}
	t.Fatal("PRD never triggered in 1000 rolls (p=0.25)")
}

func TestRoller_HardCeiling(t *testing.T) {
	r := New(0.05)
	maxN := MaxN(0.05)
	triggered := false
	for range maxN + 1 {
		if r.Roll() {
			triggered = true
			break
		}
	}
	if !triggered {
		t.Errorf("PRD did not trigger within MaxN+1=%d rolls (p=0.05)", maxN+1)
	}
}

func TestRoller_AverageRate(t *testing.T) {
	targetP := 0.25
	rng := rand.New(rand.NewPCG(42, 0))
	r := New(targetP, WithRand(rng))

	const trials = 50000
	hits := 0
	for range trials {
		if r.Roll() {
			hits++
		}
	}
	actual := float64(hits) / float64(trials)
	if math.Abs(actual-targetP) > 0.02 {
		t.Errorf("average rate = %v, want ~%v (tolerance 0.02)", actual, targetP)
	}
}

func TestRoller_LessVarianceThanTrueRandom(t *testing.T) {
	targetP := 0.25
	rng1 := rand.New(rand.NewPCG(1, 0))
	rng2 := rand.New(rand.NewPCG(1, 0))
	prdRoller := New(targetP, WithRand(rng1))

	const windowSize = 20
	const windows = 2000

	prdVar := measureVariance(windows, windowSize, func() bool { return prdRoller.Roll() })
	trueVar := measureVariance(windows, windowSize, func() bool { return rng2.Float64() < targetP })

	if prdVar >= trueVar {
		t.Errorf("PRD variance (%v) should be less than true random variance (%v)", prdVar, trueVar)
	}
}

func measureVariance(windows, windowSize int, roll func() bool) float64 {
	rates := make([]float64, windows)
	for w := range windows {
		hits := 0
		for range windowSize {
			if roll() {
				hits++
			}
		}
		rates[w] = float64(hits) / float64(windowSize)
	}
	mean := 0.0
	for _, r := range rates {
		mean += r
	}
	mean /= float64(windows)
	variance := 0.0
	for _, r := range rates {
		d := r - mean
		variance += d * d
	}
	return variance / float64(windows)
}

func TestRoller_Reset(t *testing.T) {
	r := New(0.10)
	for range 5 {
		r.Roll()
	}
	if r.Count() == 0 {
		// 极小概率所有 5 次都触发,重试
		return
	}
	r.Reset()
	if r.Count() != 0 {
		t.Errorf("after Reset, Count = %d, want 0", r.Count())
	}
}

func TestRoller_NextProb(t *testing.T) {
	r := New(0.25)
	c := CFromP(0.25)
	if math.Abs(r.NextProb()-c) > 1e-9 {
		t.Errorf("initial NextProb = %v, want ~C=%v", r.NextProb(), c)
	}
}

func TestMaxN(t *testing.T) {
	for _, p := range []float64{0.05, 0.10, 0.25, 0.50, 0.75} {
		n := MaxN(p)
		if n <= 0 {
			t.Errorf("MaxN(%v) = %d, want > 0", p, n)
		}
		if p < 1 && n < 2 {
			t.Errorf("MaxN(%v) = %d, want >= 2", p, n)
		}
	}
}
