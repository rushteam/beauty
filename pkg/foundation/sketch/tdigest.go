package sketch

import (
	"math"
	"sort"
	"sync"
)

// TDigest(t-digest,merging 变体)用常量级内存估计流式数据的**分位数**:p50/p90/p99/p999
// 以及任意 q 的近似值,误差在尾部(接近 0 或 1)极小、中部略大——正好契合监控与 SLO 对
// 「尾延迟」的关注。相比 Reservoir(等概率采样,查分位数需排样本)与直方图(需预设桶边界),
// t-digest 自适应数据分布、无需预设范围、可合并(Merge),适合分布式各实例各自统计再汇总。
//
// 原理:把数据压成若干「质心」(centroid:均值 + 权重),质心按在分布中的位置自适应控制大小——
// 越靠两端(尾部)越小越密(高精度),越靠中间越大越疏。压缩参数 compression(默认 100)越大
// 质心越多、越精确也越占内存。本实现缓冲新点、批量归并(k1 尺度函数界定质心大小)。并发安全。
type TDigest struct {
	mu          sync.Mutex
	compression float64
	means       []float64 // 已归并质心的均值,按均值升序
	weights     []float64 // 对应权重
	count       float64   // 总权重
	min, max    float64
	tmpMeans    []float64 // 未归并缓冲
	tmpWeights  []float64
	tmpCount    float64
}

// NewTDigest 创建 t-digest。compression<=0 取默认 100。
func NewTDigest(compression float64) *TDigest {
	if compression <= 0 {
		compression = 100
	}
	return &TDigest{
		compression: compression,
		min:         math.Inf(1),
		max:         math.Inf(-1),
	}
}

// Add 加入一个样本(权重 1)。
func (t *TDigest) Add(x float64) { t.AddWeighted(x, 1) }

// AddWeighted 加入一个带权样本(w>0,表示该值出现的次数/权重)。
func (t *TDigest) AddWeighted(x, w float64) {
	if w <= 0 || math.IsNaN(x) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if x < t.min {
		t.min = x
	}
	if x > t.max {
		t.max = x
	}
	t.tmpMeans = append(t.tmpMeans, x)
	t.tmpWeights = append(t.tmpWeights, w)
	t.tmpCount += w
	// 缓冲积累到一定量再批量归并,摊薄成本。
	if len(t.tmpMeans) >= 8*int(t.compression)+16 {
		t.process()
	}
}

// Count 返回已加入的总权重。
func (t *TDigest) Count() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count + t.tmpCount
}

// Quantile 返回分位数 q∈[0,1] 的估计值(如 0.99 → p99)。空集返回 NaN。
func (t *TDigest) Quantile(q float64) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.process()
	n := len(t.means)
	if n == 0 {
		return math.NaN()
	}
	if q <= 0 {
		return t.min
	}
	if q >= 1 {
		return t.max
	}
	if n == 1 {
		return t.means[0]
	}
	// 质心中心位置(累计权重坐标)。
	centers := make([]float64, n)
	cum := 0.0
	for i := 0; i < n; i++ {
		centers[i] = cum + t.weights[i]/2
		cum += t.weights[i]
	}
	target := q * t.count
	if target <= centers[0] {
		return lerp(target, 0, centers[0], t.min, t.means[0])
	}
	if target >= centers[n-1] {
		return lerp(target, centers[n-1], t.count, t.means[n-1], t.max)
	}
	for i := 0; i < n-1; i++ {
		if target <= centers[i+1] {
			return lerp(target, centers[i], centers[i+1], t.means[i], t.means[i+1])
		}
	}
	return t.max
}

// Min 返回已见最小值(空集为 +Inf)。
func (t *TDigest) Min() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.min
}

// Max 返回已见最大值(空集为 -Inf)。
func (t *TDigest) Max() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.max
}

// Merge 把 other 的质心并入本 digest(分布式汇总:各实例各自统计再合并)。
func (t *TDigest) Merge(other *TDigest) {
	other.mu.Lock()
	om, ow := append([]float64(nil), other.means...), append([]float64(nil), other.weights...)
	otm, otw := append([]float64(nil), other.tmpMeans...), append([]float64(nil), other.tmpWeights...)
	omin, omax := other.min, other.max
	other.mu.Unlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	if omin < t.min {
		t.min = omin
	}
	if omax > t.max {
		t.max = omax
	}
	for i := range om {
		t.tmpMeans = append(t.tmpMeans, om[i])
		t.tmpWeights = append(t.tmpWeights, ow[i])
		t.tmpCount += ow[i]
	}
	for i := range otm {
		t.tmpMeans = append(t.tmpMeans, otm[i])
		t.tmpWeights = append(t.tmpWeights, otw[i])
		t.tmpCount += otw[i]
	}
	t.process()
}

// Reset 清空,可复用。
func (t *TDigest) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.means, t.weights = nil, nil
	t.tmpMeans, t.tmpWeights = nil, nil
	t.count, t.tmpCount = 0, 0
	t.min, t.max = math.Inf(1), math.Inf(-1)
}

// process 把缓冲与已有质心归并:按均值排序后用 k1 尺度函数界定每个质心可吸收的权重上限。
func (t *TDigest) process() {
	if len(t.tmpMeans) == 0 {
		return
	}
	type cen struct{ m, w float64 }
	all := make([]cen, 0, len(t.means)+len(t.tmpMeans))
	for i := range t.means {
		all = append(all, cen{t.means[i], t.weights[i]})
	}
	for i := range t.tmpMeans {
		all = append(all, cen{t.tmpMeans[i], t.tmpWeights[i]})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].m < all[j].m })

	total := t.count + t.tmpCount
	newMeans := make([]float64, 0, len(all))
	newWeights := make([]float64, 0, len(all))

	cumulative := 0.0
	curM, curW := all[0].m, all[0].w
	for i := 1; i < len(all); i++ {
		q0 := cumulative / total
		q1 := (cumulative + curW + all[i].w) / total
		if kScale(q1, t.compression)-kScale(q0, t.compression) <= 1 {
			// 并入当前质心(加权均值增量更新)。
			curW += all[i].w
			curM += (all[i].m - curM) * all[i].w / curW
		} else {
			newMeans = append(newMeans, curM)
			newWeights = append(newWeights, curW)
			cumulative += curW
			curM, curW = all[i].m, all[i].w
		}
	}
	newMeans = append(newMeans, curM)
	newWeights = append(newWeights, curW)

	t.means, t.weights = newMeans, newWeights
	t.count = total
	t.tmpMeans = t.tmpMeans[:0]
	t.tmpWeights = t.tmpWeights[:0]
	t.tmpCount = 0
}

// kScale 是 t-digest 的 k1 尺度函数:把分位数 q 映射到「质心序号」空间,两端斜率大(质心密),
// 中间斜率小(质心疏)。相邻质心合并的约束是其跨度在 k 空间内 <=1。
func kScale(q, compression float64) float64 {
	// 夹紧避免 asin 域外。
	x := 2*q - 1
	if x < -1 {
		x = -1
	} else if x > 1 {
		x = 1
	}
	return compression / (2 * math.Pi) * math.Asin(x)
}

// lerp 线性插值;x1==x0 时返回 y0。
func lerp(x, x0, x1, y0, y1 float64) float64 {
	if x1 == x0 {
		return y0
	}
	return y0 + (y1-y0)*(x-x0)/(x1-x0)
}
