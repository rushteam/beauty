package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
)

// ==== Runner 策略包装器:在任意 Agent 之上再包一层运行策略。都实现 Agent,可任意嵌套。 ====

// Selector 从若干候选响应里挑一个,返回其下标(0 起)。候选均为成功跑完的响应(非 nil)。
// 判定标准是 policy,由使用方注入(如让另一个模型打分)。
type Selector func(ctx context.Context, req llm.Request, cands []*llm.Response) (int, error)

// LongestSelector 是一个平凡默认选择器:选 Content 最长的候选(并列取先者)。
func LongestSelector(_ context.Context, _ llm.Request, cands []*llm.Response) (int, error) {
	best, bestLen := 0, -1
	for i, c := range cands {
		if c != nil && len(c.Content) > bestLen {
			best, bestLen = i, len(c.Content)
		}
	}
	return best, nil
}

// BestOfN 把底层 Agent 并行跑 N 次(各次因温度/采样可能不同),再用 Select 选出最佳响应。
// N<=1 时直通(等价于底层 Agent 单跑)。Select 为 nil 时用 LongestSelector。
type BestOfN struct {
	Agent  Agent
	N      int
	Select Selector
}

var _ Agent = (*BestOfN)(nil)

// Run 并行采样 N 个候选,过滤成功者后交给 Select 选一个返回。全部失败则返回聚合错误。
func (b *BestOfN) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if b.Agent == nil {
		return nil, fmt.Errorf("agent: BestOfN has nil Agent")
	}
	n := b.N
	if n <= 1 {
		return b.Agent.Run(ctx, req)
	}

	resps := make([]*llm.Response, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resps[i], errs[i] = b.Agent.Run(ctx, req)
		}(i)
	}
	wg.Wait()

	var cands []*llm.Response
	var firstErr error
	for i := range n {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		if resps[i] != nil {
			cands = append(cands, resps[i])
		}
	}
	if len(cands) == 0 {
		if firstErr != nil {
			return nil, fmt.Errorf("agent: BestOfN all %d candidates failed: %w", n, firstErr)
		}
		return nil, fmt.Errorf("agent: BestOfN produced no candidates")
	}

	sel := b.Select
	if sel == nil {
		sel = LongestSelector
	}
	idx, err := sel(ctx, req, cands)
	if err != nil {
		return nil, fmt.Errorf("agent: BestOfN select: %w", err)
	}
	if idx < 0 || idx >= len(cands) {
		return nil, fmt.Errorf("agent: BestOfN selector returned out-of-range index %d (have %d)", idx, len(cands))
	}
	return cands[idx], nil
}

// Info 实现 Agent:透传底层 Agent 的信息。
func (b *BestOfN) Info() Info {
	if b.Agent == nil {
		return Info{}
	}
	in := b.Agent.Info()
	in.Description = fmt.Sprintf("best-of-%d(%s)", b.N, in.Description)
	return in
}

// Verifier 校验一次响应是否达标。ok=false 时 feedback 会作为新一轮的 user 消息喂回底层 Agent。
// 校验逻辑是 policy(可跑断言、跑 bash 检查、再问一个模型……),由使用方注入。
type Verifier func(ctx context.Context, resp *llm.Response) (ok bool, feedback string, err error)

// VerifyLoop 是 Ralph 式「跑→校验→带反馈重跑」循环:跑底层 Agent,若 Verify 不通过,就把 feedback
// 追加为一条 user 消息再跑,直到通过或达到 MaxRounds。MaxRounds<=0 用默认 3。
// 达到上限仍未通过时返回最后一次响应(best-effort,不报错);Verify 自身报错则中止并返回该错误。
type VerifyLoop struct {
	Agent     Agent
	Verify    Verifier
	MaxRounds int
}

var _ Agent = (*VerifyLoop)(nil)

// Run 执行校验循环,返回最终(或最后一次)响应。
func (v *VerifyLoop) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if v.Agent == nil {
		return nil, fmt.Errorf("agent: VerifyLoop has nil Agent")
	}
	rounds := v.MaxRounds
	if rounds <= 0 {
		rounds = 3
	}

	// 复制消息,避免改动调用方切片(后续会追加反馈消息)。
	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	var last *llm.Response
	for range rounds {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		req.Messages = msgs
		resp, err := v.Agent.Run(ctx, req)
		if err != nil {
			return resp, err
		}
		last = resp
		if v.Verify == nil {
			return resp, nil
		}
		ok, feedback, err := v.Verify(ctx, resp)
		if err != nil {
			return resp, fmt.Errorf("agent: VerifyLoop verify: %w", err)
		}
		if ok {
			return resp, nil
		}
		// 未通过:把上一轮答复与校验反馈追加为上下文,进入下一轮。
		msgs = append(msgs,
			llm.Message{Role: llm.Assistant, Content: resp.Content},
			llm.Message{Role: llm.User, Content: feedback},
		)
	}
	return last, nil
}

// Info 实现 Agent:透传底层 Agent 的信息。
func (v *VerifyLoop) Info() Info {
	if v.Agent == nil {
		return Info{}
	}
	in := v.Agent.Info()
	in.Description = "verify-loop(" + in.Description + ")"
	return in
}
