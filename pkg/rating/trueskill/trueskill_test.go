package trueskill_test

import (
	"math"
	"testing"

	"github.com/rushteam/beauty/pkg/rating/trueskill"
)

func TestRate1v1_DefaultWin(t *testing.T) {
	// python-trueskill / Microsoft 公开算例:两名默认玩家,前者胜。
	// Rating(mu=29.396, sigma=7.171) vs Rating(mu=20.604, sigma=7.171)
	c := trueskill.New()
	w, l := c.Rate1v1(trueskill.Default(), trueskill.Default(), false)
	if math.Abs(w.Mu-29.396) > 0.05 {
		t.Errorf("winner μ = %.4f, want ~29.396", w.Mu)
	}
	if math.Abs(l.Mu-20.604) > 0.05 {
		t.Errorf("loser μ = %.4f, want ~20.604", l.Mu)
	}
	if math.Abs(w.Sigma-7.171) > 0.05 {
		t.Errorf("winner σ = %.4f, want ~7.171", w.Sigma)
	}
	if math.Abs(l.Sigma-7.171) > 0.05 {
		t.Errorf("loser σ = %.4f, want ~7.171", l.Sigma)
	}
}

func TestRate1v1_Draw(t *testing.T) {
	c := trueskill.New()
	a, b := c.Rate1v1(trueskill.Default(), trueskill.Default(), true)
	if math.Abs(a.Mu-b.Mu) > 1e-9 {
		t.Fatalf("draw should keep μ equal: %.4f vs %.4f", a.Mu, b.Mu)
	}
	if a.Sigma >= trueskill.Default().Sigma {
		t.Fatalf("σ should shrink after a game, got %.4f", a.Sigma)
	}
}

func TestRate1v1_StrongerBeatsWeakerLessGain(t *testing.T) {
	c := trueskill.New()
	strong := trueskill.Rating{Mu: 35, Sigma: 7}
	weak := trueskill.Rating{Mu: 15, Sigma: 7}
	upsetW, _ := c.Rate1v1(weak, strong, false)
	favW, _ := c.Rate1v1(strong, weak, false)
	upsetGain := upsetW.Mu - weak.Mu
	favGain := favW.Mu - strong.Mu
	if upsetGain <= favGain {
		t.Fatalf("upset should gain more: upset=%.3f fav=%.3f", upsetGain, favGain)
	}
}

func TestRateTeams_TwoVsTwo(t *testing.T) {
	c := trueskill.New()
	d := trueskill.Default()
	out := c.RateTeams(
		[][]trueskill.Rating{{d, d}, {d, d}},
		[]int{0, 1},
	)
	if len(out) != 2 || len(out[0]) != 2 || len(out[1]) != 2 {
		t.Fatalf("shape: %+v", out)
	}
	if out[0][0].Mu <= d.Mu {
		t.Fatalf("winning team μ should rise, got %.3f", out[0][0].Mu)
	}
	if out[1][0].Mu >= d.Mu {
		t.Fatalf("losing team μ should fall, got %.3f", out[1][0].Mu)
	}
}

func TestRateTeams_ThreeWay(t *testing.T) {
	c := trueskill.New()
	d := trueskill.Default()
	out := c.RateTeams(
		[][]trueskill.Rating{{d}, {d}, {d}},
		[]int{0, 1, 2},
	)
	if out[0][0].Mu <= out[1][0].Mu || out[1][0].Mu <= out[2][0].Mu {
		t.Fatalf("ranks should order μ: %.3f %.3f %.3f", out[0][0].Mu, out[1][0].Mu, out[2][0].Mu)
	}
}

func TestRateTeams_MismatchedRanks(t *testing.T) {
	c := trueskill.New()
	d := trueskill.Default()
	in := [][]trueskill.Rating{{d}, {d}}
	out := c.RateTeams(in, []int{0})
	if out[0][0] != d || out[1][0] != d {
		t.Fatalf("mismatch should return clone of input, got %+v", out)
	}
}

func TestOrdinal(t *testing.T) {
	c := trueskill.New()
	r := trueskill.Rating{Mu: 25, Sigma: 25.0 / 3.0}
	want := 25 - 3*(25.0/3.0) // = 0
	if math.Abs(c.Ordinal(r)-want) > 1e-9 {
		t.Fatalf("Ordinal = %.4f, want %.4f", c.Ordinal(r), want)
	}
}

func TestMatchQuality1v1(t *testing.T) {
	c := trueskill.New()
	eq := c.MatchQuality1v1(trueskill.Default(), trueskill.Default())
	far := c.MatchQuality1v1(
		trueskill.Rating{Mu: 40, Sigma: 8.3},
		trueskill.Rating{Mu: 10, Sigma: 8.3},
	)
	if eq <= far {
		t.Fatalf("equal players should have higher quality: eq=%.4f far=%.4f", eq, far)
	}
	if eq <= 0 || eq > 1 {
		t.Fatalf("quality out of range: %.4f", eq)
	}
}

func TestNormalize_ZeroValue(t *testing.T) {
	c := trueskill.New()
	w, l := c.Rate1v1(trueskill.Rating{}, trueskill.Rating{}, false)
	if w.Mu <= 25 {
		t.Fatalf("zero-value winner should gain, μ=%.3f", w.Mu)
	}
	if l.Mu >= 25 {
		t.Fatalf("zero-value loser should drop, μ=%.3f", l.Mu)
	}
}

func TestWithDrawProbabilityZero(t *testing.T) {
	c := trueskill.New(trueskill.WithDrawProbability(0))
	w, l := c.Rate1v1(trueskill.Default(), trueskill.Default(), false)
	if w.Mu <= l.Mu {
		t.Fatalf("winner should still be ahead: %.3f vs %.3f", w.Mu, l.Mu)
	}
}
