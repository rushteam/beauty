package glicko2_test

import (
	"math"
	"testing"

	"github.com/rushteam/beauty/pkg/rating/glicko2"
)

func TestRate_GlickmanExample(t *testing.T) {
	// 论文算例:玩家 1500/200/0.06,对手 (1400,30,胜)、(1550,100,负)、(1700,300,负)。
	// 期望约 r=1464.06, RD=151.52, σ=0.05999。
	c := glicko2.New()
	player := glicko2.Rating{R: 1500, RD: 200, Sig: 0.06}
	got := c.Rate(player, []glicko2.Outcome{
		{Opponent: glicko2.Rating{R: 1400, RD: 30, Sig: 0.06}, Score: 1},
		{Opponent: glicko2.Rating{R: 1550, RD: 100, Sig: 0.06}, Score: 0},
		{Opponent: glicko2.Rating{R: 1700, RD: 300, Sig: 0.06}, Score: 0},
	})
	if math.Abs(got.R-1464.06) > 0.05 {
		t.Errorf("R = %.4f, want ~1464.06", got.R)
	}
	if math.Abs(got.RD-151.52) > 0.05 {
		t.Errorf("RD = %.4f, want ~151.52", got.RD)
	}
	if math.Abs(got.Sig-0.05999) > 1e-4 {
		t.Errorf("Sig = %.6f, want ~0.05999", got.Sig)
	}
}

func TestRate_WinnerGains(t *testing.T) {
	c := glicko2.New()
	a := glicko2.Default()
	b := glicko2.Default()
	a2 := c.Rate(a, []glicko2.Outcome{{Opponent: b, Score: 1}})
	b2 := c.Rate(b, []glicko2.Outcome{{Opponent: a, Score: 0}})
	if a2.R <= a.R {
		t.Fatalf("winner R should increase: %.2f → %.2f", a.R, a2.R)
	}
	if b2.R >= b.R {
		t.Fatalf("loser R should decrease: %.2f → %.2f", b.R, b2.R)
	}
	if a2.RD >= a.RD || b2.RD >= b.RD {
		t.Fatalf("RD should shrink after a game: a %.2f→%.2f b %.2f→%.2f", a.RD, a2.RD, b.RD, b2.RD)
	}
}

func TestRate_DrawUnchanged(t *testing.T) {
	c := glicko2.New()
	a := glicko2.Default()
	b := glicko2.Default()
	a2 := c.Rate(a, []glicko2.Outcome{{Opponent: b, Score: 0.5}})
	if math.Abs(a2.R-a.R) > 0.5 {
		t.Fatalf("equal draw should keep R near 1500, got %.2f", a2.R)
	}
}

func TestRate_EmptyOutcomesDecays(t *testing.T) {
	c := glicko2.New()
	p := glicko2.Rating{R: 1600, RD: 50, Sig: 0.06}
	got := c.Rate(p, nil)
	want := c.Decay(p)
	if got != want {
		t.Fatalf("empty Rate = %+v, want Decay %+v", got, want)
	}
}

func TestDecay_RDGrowsAndCaps(t *testing.T) {
	c := glicko2.New()
	p := glicko2.Rating{R: 1500, RD: 50, Sig: 0.06}
	d := c.Decay(p)
	if d.R != 1500 {
		t.Fatalf("R changed: %.2f", d.R)
	}
	if d.RD <= p.RD {
		t.Fatalf("RD should grow, got %.4f", d.RD)
	}
	capped := p
	capped.RD = 350
	d2 := c.Decay(capped)
	if d2.RD > 350 {
		t.Fatalf("RD cap broken: %.2f", d2.RD)
	}
}

func TestOrdinal(t *testing.T) {
	c := glicko2.New()
	r := glicko2.Rating{R: 1500, RD: 200, Sig: 0.06}
	if got := c.Ordinal(r); got != 1100 {
		t.Fatalf("Ordinal = %.1f, want 1100", got)
	}
}

func TestNormalize_ZeroValue(t *testing.T) {
	c := glicko2.New()
	got := c.Rate(glicko2.Rating{}, []glicko2.Outcome{
		{Opponent: glicko2.Rating{}, Score: 1},
	})
	if got.R <= 1500 {
		t.Fatalf("zero-value player beating default should gain, R=%.2f", got.R)
	}
}

func TestWithTau(t *testing.T) {
	p := glicko2.Rating{R: 1500, RD: 200, Sig: 0.06}
	outcomes := []glicko2.Outcome{
		{Opponent: glicko2.Rating{R: 1400, RD: 30, Sig: 0.06}, Score: 1},
	}
	low := glicko2.New(glicko2.WithTau(0.3)).Rate(p, outcomes)
	high := glicko2.New(glicko2.WithTau(1.2)).Rate(p, outcomes)
	if low.R == 0 || high.R == 0 {
		t.Fatal("empty rating")
	}
}
