// Package glicko2 提供 Glicko-2 评分原语:根据一组对局结果更新玩家的评分、
// 评分偏差与波动性。纯计算、无状态、并发安全。
//
// 算法来自 Mark Glickman《Example of the Glicko-2 system》(2013)。内部在
// Glicko-2 尺度 (μ, φ) 上运算,对外仍使用习惯的 (R, RD, σ):
//
//	R  评分,默认 1500
//	RD 评分偏差(越大越不确定),默认 350,上限 350
//	σ  波动性,默认 0.06
//
// 典型用法:对局结束后用 Rate 得到新 Rating,再把 Ordinal(R-2*RD,保守估计)
// 写入 matchmaker ticket 的 skill 属性。长时间不打则调用 Decay 膨胀 RD。
//
// 零值 Calculator 不可用,用 New 构造。
package glicko2

import "math"

const (
	defaultR   = 1500.0
	defaultRD  = 350.0
	defaultSig = 0.06
	defaultTau = 0.5
	defaultEps = 1e-6
	scale      = 173.7178 // 400 / ln(10),Glicko ↔ Glicko-2 换算
	maxRD      = 350.0
	maxIter    = 100
)

// Rating 一名玩家的 Glicko-2 评分三元组。
type Rating struct {
	R   float64 // 评分
	RD  float64 // 评分偏差
	Sig float64 // 波动性
}

// Outcome 相对一名对手的一局结果。
type Outcome struct {
	Opponent Rating
	Score    float64 // 1.0=胜,0.5=平,0.0=负
}

// Default 返回未评级玩家的默认 Rating(1500/350/0.06)。
func Default() Rating {
	return Rating{R: defaultR, RD: defaultRD, Sig: defaultSig}
}

type config struct {
	tau float64
	eps float64
}

// Option 配置 Calculator。
type Option func(*config)

// WithTau 设置系统常数 τ,控制波动性变化速率。论文建议 0.3~1.2,默认 0.5。
func WithTau(tau float64) Option {
	return func(c *config) {
		if tau > 0 {
			c.tau = tau
		}
	}
}

// WithEpsilon 设置 Illinois 迭代的收敛阈值,默认 1e-6。
func WithEpsilon(eps float64) Option {
	return func(c *config) {
		if eps > 0 {
			c.eps = eps
		}
	}
}

// Calculator Glicko-2 计算器。无状态,并发安全。
type Calculator struct {
	tau float64
	eps float64
}

// New 创建计算器。
func New(opts ...Option) *Calculator {
	cfg := config{tau: defaultTau, eps: defaultEps}
	for _, o := range opts {
		o(&cfg)
	}
	return &Calculator{tau: cfg.tau, eps: cfg.eps}
}

// Rate 根据本评分周期内的一组对局更新 player。outcomes 为空时等价于 Decay。
func (c *Calculator) Rate(player Rating, outcomes []Outcome) Rating {
	player = normalize(player)
	if len(outcomes) == 0 {
		return c.Decay(player)
	}

	mu := (player.R - defaultR) / scale
	phi := player.RD / scale
	sigma := player.Sig

	gE := make([][2]float64, len(outcomes)) // [g, E]
	var sumV, sumDelta float64
	for i, o := range outcomes {
		opp := normalize(o.Opponent)
		muJ := (opp.R - defaultR) / scale
		phiJ := opp.RD / scale
		gJ := gPhi(phiJ)
		e := expect(mu, muJ, gJ)
		gE[i] = [2]float64{gJ, e}
		sumV += gJ * gJ * e * (1 - e)
		score := o.Score
		if score < 0 {
			score = 0
		} else if score > 1 {
			score = 1
		}
		sumDelta += gJ * (score - e)
	}
	if sumV <= 0 {
		return c.Decay(player)
	}
	v := 1 / sumV
	delta := v * sumDelta

	sigmaP := c.newSigma(sigma, phi, v, delta)
	phiStar := math.Sqrt(phi*phi + sigmaP*sigmaP)
	phiP := 1 / math.Sqrt(1/(phiStar*phiStar)+1/v)
	muP := mu + phiP*phiP*sumDelta

	rd := phiP * scale
	if rd > maxRD {
		rd = maxRD
	}
	return Rating{R: scale*muP + defaultR, RD: rd, Sig: sigmaP}
}

// Decay 处理一个无对局的评分周期:R/σ 不变,RD 随波动性膨胀(上限 350)。
func (c *Calculator) Decay(player Rating) Rating {
	player = normalize(player)
	phi := player.RD / scale
	phiP := math.Sqrt(phi*phi + player.Sig*player.Sig)
	rd := phiP * scale
	if rd > maxRD {
		rd = maxRD
	}
	return Rating{R: player.R, RD: rd, Sig: player.Sig}
}

// Ordinal 返回写入 matchmaker skill 的保守估计:R - 2*RD。
func (c *Calculator) Ordinal(r Rating) float64 {
	r = normalize(r)
	return r.R - 2*r.RD
}

func normalize(r Rating) Rating {
	if r.R == 0 && r.RD == 0 && r.Sig == 0 {
		return Default()
	}
	if r.RD <= 0 {
		r.RD = defaultRD
	}
	if r.Sig <= 0 {
		r.Sig = defaultSig
	}
	if r.RD > maxRD {
		r.RD = maxRD
	}
	return r
}

func gPhi(phi float64) float64 {
	return 1 / math.Sqrt(1+3*phi*phi/(math.Pi*math.Pi))
}

func expect(mu, muJ, gJ float64) float64 {
	return 1 / (1 + math.Exp(-gJ*(mu-muJ)))
}

// newSigma 用 Illinois 算法求新波动性(论文 Step 5)。
func (c *Calculator) newSigma(sigma, phi, v, delta float64) float64 {
	a := math.Log(sigma * sigma)
	tau := c.tau
	eps := c.eps
	delta2 := delta * delta
	phi2 := phi * phi

	f := func(x float64) float64 {
		ex := math.Exp(x)
		num := ex * (delta2 - phi2 - v - ex)
		den := 2 * (phi2 + v + ex) * (phi2 + v + ex)
		return num/den - (x-a)/(tau*tau)
	}

	A := a
	var B float64
	if delta2 > phi2+v {
		B = math.Log(delta2 - phi2 - v)
	} else {
		k := 1.0
		for f(a-k*tau) < 0 {
			k++
			if k > maxIter {
				break
			}
		}
		B = a - k*tau
	}

	fA, fB := f(A), f(B)
	for range maxIter {
		if math.Abs(B-A) <= eps {
			break
		}
		C := A + (A-B)*fA/(fB-fA)
		fC := f(C)
		if fC*fB <= 0 {
			A = B
			fA = fB
		} else {
			fA /= 2
		}
		B = C
		fB = fC
	}
	return math.Exp(A / 2)
}
