package llm

import (
	"context"
	"iter"
	"slices"
	"sync"
	"time"
)

// ModelRole 标识模型在一次 agent 运行中的功能角色,用于成本归因。
// 同一个模型可能在不同角色下被调用(如主模型 + 压缩模型),token 消耗分开计量。
type ModelRole string

const (
	RolePrimary     ModelRole = "primary"     // 主对话模型
	RoleCompression ModelRole = "compression" // 工具结果压缩
	RoleSummarizer  ModelRole = "summarizer"  // 会话摘要
	RolePlanner     ModelRole = "planner"     // 规划/推理
	RoleEvaluator   ModelRole = "evaluator"   // 评估/打分
	RoleLearning    ModelRole = "learning"    // 学习/记忆提取
	RoleEmbedding   ModelRole = "embedding"   // 向量化
	RoleGuardrail   ModelRole = "guardrail"   // 护栏检查
)

// RoleUsage 记录某个角色下的模型用量。
type RoleUsage struct {
	Role         ModelRole     `json:"role"`
	Model        string        `json:"model"`
	Usage        Usage         `json:"usage"`
	Calls        int           `json:"calls"`         // 调用次数
	TotalLatency time.Duration `json:"total_latency"` // 累计延迟
}

// CostTracker 按角色追踪 token 消耗。并发安全。
type CostTracker struct {
	mu     sync.RWMutex
	usages map[ModelRole]*RoleUsage
}

// NewCostTracker 创建成本追踪器。
func NewCostTracker() *CostTracker {
	return &CostTracker{usages: make(map[ModelRole]*RoleUsage)}
}

// Record 记录一次模型调用的用量。
func (t *CostTracker) Record(role ModelRole, model string, usage Usage, latency time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ru, ok := t.usages[role]
	if !ok {
		ru = &RoleUsage{Role: role, Model: model}
		t.usages[role] = ru
	} else if model != "" {
		ru.Model = model
	}
	addUsage(&ru.Usage, usage)
	ru.Calls++
	ru.TotalLatency += latency
}

// Summary 返回所有角色的用量汇总(快照副本),按 role 字典序排序。
func (t *CostTracker) Summary() []RoleUsage {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]RoleUsage, 0, len(t.usages))
	for _, ru := range t.usages {
		out = append(out, *ru)
	}
	slices.SortFunc(out, func(a, b RoleUsage) int {
		if a.Role < b.Role {
			return -1
		}
		if a.Role > b.Role {
			return 1
		}
		return 0
	})
	return out
}

// Total 返回总 token 数(input + output)。
func (t *CostTracker) Total() (input, output int) {
	if t == nil {
		return 0, 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, ru := range t.usages {
		input += ru.Usage.InputTokens
		output += ru.Usage.OutputTokens
	}
	return input, output
}

// Reset 清空所有记录。
func (t *CostTracker) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	clear(t.usages)
}

func addUsage(dst *Usage, src Usage) {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheCreationInputTokens += src.CacheCreationInputTokens
	dst.CacheReadInputTokens += src.CacheReadInputTokens
}

type costTrackerKey struct{}

// WithCostTracker 在 context 上附加 CostTracker。
func WithCostTracker(ctx context.Context, tracker *CostTracker) context.Context {
	return context.WithValue(ctx, costTrackerKey{}, tracker)
}

// CostTrackerFrom 从 context 获取 CostTracker。
func CostTrackerFrom(ctx context.Context) *CostTracker {
	if t, ok := ctx.Value(costTrackerKey{}).(*CostTracker); ok {
		return t
	}
	return nil
}

// WithRole 包装一个 Client,标记其所有调用为指定角色,并记录到 CostTracker。
// 这是 Metered 的增强版:Metered 只回调 hook,WithRole 直接写入 CostTracker。
func WithRole(c Client, tracker *CostTracker, role ModelRole) Client {
	if tracker == nil {
		return c
	}
	return &roleClient{c: c, tracker: tracker, role: role}
}

type roleClient struct {
	c       Client
	tracker *CostTracker
	role    ModelRole
}

func (r *roleClient) Generate(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()
	resp, err := r.c.Generate(ctx, req)
	if err == nil {
		model := resp.Model
		if model == "" {
			model = req.Model
		}
		r.tracker.Record(r.role, model, resp.Usage, time.Since(start))
	}
	return resp, err
}

func (r *roleClient) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		start := time.Now()
		var usage Usage
		for chunk, err := range r.c.Stream(ctx, req) {
			if err != nil {
				yield(chunk, err)
				return
			}
			if chunk.Usage != nil {
				usage = *chunk.Usage
			}
			if !yield(chunk, nil) {
				r.recordStream(req.Model, usage, start)
				return
			}
		}
		r.recordStream(req.Model, usage, start)
	}
}

func (r *roleClient) recordStream(fallbackModel string, usage Usage, start time.Time) {
	r.tracker.Record(r.role, fallbackModel, usage, time.Since(start))
}
