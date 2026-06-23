// Package featureflag 提供本地评估的特性开关与 A/B 实验：
// 布尔开关、百分比灰度、按属性定向、多变体实验，均基于确定性哈希分桶，
// 同一标识（如 userID）多次评估结果稳定，无需远程调用。
package featureflag

import (
	"hash/fnv"
	"sync"
)

// Attributes 是用于定向匹配的标识属性（如 {"country":"CN","plan":"pro"}）。
type Attributes map[string]any

// Rule 是一条定向规则：When 全部命中时，强制结果为 Force。
type Rule struct {
	When  map[string]any // 属性等值匹配，全部满足才算命中
	Force bool           // 命中后强制开/关
}

// Flag 是一个特性开关定义。
type Flag struct {
	Key     string
	Enabled bool    // 总开关；false 时一律关闭（除非被 Rule 命中强制开）
	Rollout float64 // 0~1 灰度比例，Enabled 且未命中规则时按此比例放量；0 表示用默认 1
	Rules   []Rule  // 定向规则，按顺序首个命中生效
}

// Experiment 是一个 A/B(/n) 实验定义。
type Experiment struct {
	Key      string
	Variants []string  // 变体名，如 ["control","treatment"]
	Weights  []float64 // 各变体权重，缺省或非法时等分；和应约等于 1
	Coverage float64   // 0~1 参与实验的比例，0 表示用默认 1
}

// Engine 持有开关与实验配置，提供线程安全的评估。
type Engine struct {
	mu    sync.RWMutex
	flags map[string]Flag
	exps  map[string]Experiment
}

// New 创建一个空 Engine。
func New() *Engine {
	return &Engine{
		flags: make(map[string]Flag),
		exps:  make(map[string]Experiment),
	}
}

// SetFlag 注册/更新一个开关。
func (e *Engine) SetFlag(f Flag) {
	e.mu.Lock()
	e.flags[f.Key] = f
	e.mu.Unlock()
}

// SetExperiment 注册/更新一个实验。
func (e *Engine) SetExperiment(x Experiment) {
	e.mu.Lock()
	e.exps[x.Key] = x
	e.mu.Unlock()
}

// IsEnabled 评估某标识 id 是否命中开关 key。attrs 可为 nil。
// 评估顺序：定向规则（首个命中）→ 总开关 → 百分比灰度（按 id 稳定分桶）。
func (e *Engine) IsEnabled(key, id string, attrs Attributes) bool {
	e.mu.RLock()
	f, ok := e.flags[key]
	e.mu.RUnlock()
	if !ok {
		return false
	}
	for _, r := range f.Rules {
		if matchRule(r.When, attrs) {
			return r.Force
		}
	}
	if !f.Enabled {
		return false
	}
	rollout := f.Rollout
	if rollout <= 0 {
		rollout = 1
	}
	if rollout >= 1 {
		return true
	}
	return bucket(key, id) < rollout
}

// Variant 评估某标识 id 在实验 key 中的变体。
// 返回 (变体名, true)；未参与实验（超出 coverage）或实验不存在时返回 ("", false)，
// 调用方应回退到对照/默认行为。
func (e *Engine) Variant(key, id string) (string, bool) {
	e.mu.RLock()
	x, ok := e.exps[key]
	e.mu.RUnlock()
	if !ok || len(x.Variants) == 0 {
		return "", false
	}
	coverage := x.Coverage
	if coverage <= 0 {
		coverage = 1
	}

	n := bucket(key, id)
	if n >= coverage {
		return "", false // 未纳入实验
	}
	// 在 coverage 内按权重分配；把 n 归一化到 [0,1) 再选桶
	weights := normalizeWeights(x.Weights, len(x.Variants))
	pos := n / coverage
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if pos < cumulative {
			return x.Variants[i], true
		}
	}
	return x.Variants[len(x.Variants)-1], true
}

func matchRule(when map[string]any, attrs Attributes) bool {
	if len(when) == 0 {
		return false // 空条件不视为命中，避免误强制
	}
	for k, want := range when {
		if attrs[k] != want {
			return false
		}
	}
	return true
}

func normalizeWeights(weights []float64, n int) []float64 {
	if n <= 0 {
		return nil
	}
	valid := len(weights) == n
	total := 0.0
	if valid {
		for _, w := range weights {
			if w < 0 {
				valid = false
				break
			}
			total += w
		}
		if total < 0.99 || total > 1.01 {
			valid = false
		}
	}
	if !valid {
		eq := make([]float64, n)
		for i := range eq {
			eq[i] = 1.0 / float64(n)
		}
		return eq
	}
	return weights
}

// bucket 把 (key, id) 稳定映射到 [0,1)，用于灰度/分桶。
func bucket(key, id string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	_, _ = h.Write([]byte(":"))
	_, _ = h.Write([]byte(key))
	return float64(h.Sum32()%10000) / 10000
}
