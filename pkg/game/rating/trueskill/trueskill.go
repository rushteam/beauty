// Package trueskill 提供 TrueSkill 评分原语:用高斯分布 (μ, σ) 表示玩家实力,
// 根据 1v1 或多队伍对局结果做贝叶斯更新。纯计算、无状态、并发安全。
//
// 默认先验与 Microsoft TrueSkill 一致:
//
//	μ₀ = 25, σ₀ = 25/3, β = σ₀/2, τ = σ₀/100, 平局概率 0.10
//
// Rate1v1 / RateTeams 使用两队闭式解(截断高斯);多队伍按名次排序后对相邻
// 名次做两两更新(链式近似,1v1 与两队情形退化为精确闭式解)。
//
// Ordinal 返回 μ-3σ(保守估计,约 99.7% 下限),可写入 matchmaker skill。
// MatchQuality1v1 返回 0~1 的匹配质量,1 表示实力最接近。
//
// 零值 Calculator 不可用,用 New 构造。
package trueskill

import "math"

const (
	defaultMu       = 25.0
	defaultSigma    = 25.0 / 3.0
	defaultBeta     = defaultSigma / 2.0
	defaultTau      = defaultSigma / 100.0
	defaultDrawProb = 0.10
	minVariance     = 1e-6
)

// Rating 一名玩家的 TrueSkill 高斯评分。
type Rating struct {
	Mu    float64 // 均值
	Sigma float64 // 标准差
}

// Default 返回未评级玩家的默认 Rating(μ=25, σ=25/3)。
func Default() Rating {
	return Rating{Mu: defaultMu, Sigma: defaultSigma}
}

type config struct {
	mu0, sigma0, beta, tau, drawProb float64
}

// Option 配置 Calculator。
type Option func(*config)

// WithMu 设置先验均值,默认 25。
func WithMu(mu float64) Option {
	return func(c *config) {
		if mu > 0 {
			c.mu0 = mu
		}
	}
}

// WithSigma 设置先验标准差,默认 25/3。
func WithSigma(sigma float64) Option {
	return func(c *config) {
		if sigma > 0 {
			c.sigma0 = sigma
		}
	}
}

// WithBeta 设置表现方差因子(运气),默认 σ₀/2。
func WithBeta(beta float64) Option {
	return func(c *config) {
		if beta > 0 {
			c.beta = beta
		}
	}
}

// WithTau 设置动态因子,每局给 σ² 加上 τ²,默认 σ₀/100。
func WithTau(tau float64) Option {
	return func(c *config) {
		if tau >= 0 {
			c.tau = tau
		}
	}
}

// WithDrawProbability 设置平局先验概率,用于计算平局边界 ε,默认 0.10。
func WithDrawProbability(p float64) Option {
	return func(c *config) {
		if p >= 0 && p < 1 {
			c.drawProb = p
		}
	}
}

// Calculator TrueSkill 计算器。无状态,并发安全。
type Calculator struct {
	mu0, sigma0, beta, tau, drawProb float64
}

// New 创建计算器。
func New(opts ...Option) *Calculator {
	cfg := config{
		mu0:      defaultMu,
		sigma0:   defaultSigma,
		beta:     defaultBeta,
		tau:      defaultTau,
		drawProb: defaultDrawProb,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.beta <= 0 {
		cfg.beta = cfg.sigma0 / 2
	}
	return &Calculator{
		mu0:      cfg.mu0,
		sigma0:   cfg.sigma0,
		beta:     cfg.beta,
		tau:      cfg.tau,
		drawProb: cfg.drawProb,
	}
}

// Rate1v1 更新一局 1v1。draw=true 时按平局更新。
func (c *Calculator) Rate1v1(winner, loser Rating, draw bool) (Rating, Rating) {
	teams := [][]Rating{{winner}, {loser}}
	ranks := []int{0, 1}
	if draw {
		ranks = []int{0, 0}
	}
	out := c.RateTeams(teams, ranks)
	return out[0][0], out[1][0]
}

// RateTeams 按队伍名次更新。teams[i] 是第 i 队的队员评分,ranks[i] 是该队名次
// (越小越好,并列表示平局)。返回与 teams 同形的新评分;ranks 长度不匹配时原样返回。
func (c *Calculator) RateTeams(teams [][]Rating, ranks []int) [][]Rating {
	if len(teams) == 0 || len(teams) != len(ranks) {
		return cloneTeams(teams)
	}
	out := make([][]Rating, len(teams))
	for i, team := range teams {
		out[i] = make([]Rating, len(team))
		for j, r := range team {
			out[i][j] = c.normalize(r)
			out[i][j].Sigma = math.Sqrt(out[i][j].Sigma*out[i][j].Sigma + c.tau*c.tau)
		}
	}
	if len(out) == 1 {
		return out
	}

	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	// 按名次升序,稳定。
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if ranks[order[j]] < ranks[order[i]] {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	for k := 0; k < len(order)-1; k++ {
		a, b := order[k], order[k+1]
		draw := ranks[a] == ranks[b]
		c.updateTwoTeams(out[a], out[b], draw)
	}
	return out
}

// Ordinal 返回 μ-3σ 保守估计,用作 matchmaker skill。
func (c *Calculator) Ordinal(r Rating) float64 {
	r = c.normalize(r)
	return r.Mu - 3*r.Sigma
}

// MatchQuality1v1 返回两名玩家的匹配质量 [0,1],1 表示实力最接近。
func (c *Calculator) MatchQuality1v1(a, b Rating) float64 {
	a, b = c.normalize(a), c.normalize(b)
	sigmaSq := 2 * c.beta * c.beta
	den := sigmaSq + a.Sigma*a.Sigma + b.Sigma*b.Sigma
	if den <= 0 {
		return 0
	}
	dmu := a.Mu - b.Mu
	return math.Sqrt(sigmaSq/den) * math.Exp(-(dmu*dmu)/(2*den))
}

func (c *Calculator) normalize(r Rating) Rating {
	if r.Mu == 0 && r.Sigma == 0 {
		return Default()
	}
	if r.Sigma <= 0 {
		r.Sigma = c.sigma0
	}
	return r
}

// updateTwoTeams 两队闭式更新。winner 胜(或平局时与 loser 对称)。
func (c *Calculator) updateTwoTeams(winner, loser []Rating, draw bool) {
	if len(winner) == 0 || len(loser) == 0 {
		return
	}
	var muW, muL, varW, varL float64
	for _, r := range winner {
		muW += r.Mu
		varW += r.Sigma * r.Sigma
	}
	for _, r := range loser {
		muL += r.Mu
		varL += r.Sigma * r.Sigma
	}
	n := float64(len(winner) + len(loser))
	c2 := varW + varL + n*c.beta*c.beta
	if c2 <= 0 {
		return
	}
	cVal := math.Sqrt(c2)
	t := (muW - muL) / cVal
	eps := c.drawMargin(n) / cVal

	var v, w float64
	if draw {
		v, w = vDraw(t, eps), wDraw(t, eps)
	} else {
		v, w = vWin(t, eps), wWin(t, eps)
	}

	apply := func(team []Rating, sign float64) {
		for i := range team {
			s2 := team[i].Sigma * team[i].Sigma
			team[i].Mu += sign * (s2 / cVal) * v
			ns2 := s2 * (1 - (s2/c2)*w)
			if ns2 < minVariance {
				ns2 = minVariance
			}
			team[i].Sigma = math.Sqrt(ns2)
		}
	}
	if draw {
		apply(winner, 1)
		apply(loser, -1)
	} else {
		apply(winner, 1)
		apply(loser, -1)
	}
}

func (c *Calculator) drawMargin(nPlayers float64) float64 {
	if c.drawProb <= 0 {
		return 0
	}
	return invCDF((c.drawProb+1)/2) * math.Sqrt(nPlayers) * c.beta
}

func cloneTeams(teams [][]Rating) [][]Rating {
	out := make([][]Rating, len(teams))
	for i, team := range teams {
		out[i] = append([]Rating(nil), team...)
	}
	return out
}

func pdf(x float64) float64 {
	return math.Exp(-0.5*x*x) / math.Sqrt(2*math.Pi)
}

func cdf(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

const tiny = 2.222e-16

func vWin(diff, drawMargin float64) float64 {
	x := diff - drawMargin
	d := cdf(x)
	if d < tiny {
		if x < 0 {
			return -x
		}
		return 0
	}
	return pdf(x) / d
}

func wWin(diff, drawMargin float64) float64 {
	x := diff - drawMargin
	v := vWin(diff, drawMargin)
	d := cdf(x)
	if d < tiny {
		if x < 0 {
			return 1
		}
		return 0
	}
	return v * (v + x)
}

func vDraw(diff, drawMargin float64) float64 {
	abs := math.Abs(diff)
	a := drawMargin - abs
	b := -drawMargin - abs
	d := cdf(a) - cdf(b)
	if d < tiny {
		if abs < 1e-8 {
			return 0
		}
		return -diff
	}
	v := (pdf(b) - pdf(a)) / d
	if diff < 0 {
		return -v
	}
	return v
}

func wDraw(diff, drawMargin float64) float64 {
	abs := math.Abs(diff)
	a := drawMargin - abs
	b := -drawMargin - abs
	d := cdf(a) - cdf(b)
	if d < tiny {
		return 1
	}
	v := vDraw(diff, drawMargin)
	return v*v + (a*pdf(a)-b*pdf(b))/d
}

// invCDF 标准正态分位数(Acklam 有理逼近)。
func invCDF(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	const (
		a1    = -3.969683028665376e+01
		a2    = 2.209460984245205e+02
		a3    = -2.759285104469687e+02
		a4    = 1.383577509590705e+02
		a5    = -3.066479806614716e+01
		a6    = 2.506628277459239e+00
		b1    = -5.447609879822406e+01
		b2    = 1.615858368580409e+02
		b3    = -1.556989798598866e+02
		b4    = 6.680131188771972e+01
		b5    = -1.328068155288572e+01
		c1    = -7.784894002430293e-03
		c2    = -3.223964580411365e-01
		c3    = -2.400758277161838e+00
		c4    = -2.549732539343734e+00
		c5    = 4.374664141464968e+00
		c6    = 2.938163469594125e+00
		d1    = 7.784695709041462e-03
		d2    = 3.224671290700398e-01
		d3    = 2.445134137142996e+00
		d4    = 3.754408661907416e+00
		pLow  = 0.02425
		pHigh = 1 - pLow
	)
	if p < pLow {
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	}
	if p <= pHigh {
		q := p - 0.5
		r := q * q
		return (((((a1*r+a2)*r+a3)*r+a4)*r+a5)*r + a6) * q /
			(((((b1*r+b2)*r+b3)*r+b4)*r+b5)*r + 1)
	}
	q := math.Sqrt(-2 * math.Log(1-p))
	return -(((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
		((((d1*q+d2)*q+d3)*q+d4)*q + 1)
}
