package agent

import (
	"context"
	"fmt"

	"github.com/rushteam/beauty/contrib/llm"
)

// SyncHITL 把可暂停的 Agent 包成「进程内阻塞审批」适配器:Paused 时对每个 Requirement
// 调用 approve,填 Resolution 后 Continue,直到 Done 或 Error。
// approve 为 nil 时,Paused 直接以 Error(ErrPaused) 返回。
//
// 这是策略适配器,不进 Runner 核心。适合脚本/测试;产品路径应显式 Continue。
func SyncHITL(inner Agent, approve func(ctx context.Context, tc llm.ToolCall) (Resolution, error)) Agent {
	return &syncHITL{inner: inner, approve: approve}
}

type syncHITL struct {
	inner   Agent
	approve func(ctx context.Context, tc llm.ToolCall) (Resolution, error)
}

func (s *syncHITL) Info() Info { return s.inner.Info() }

func (s *syncHITL) Run(ctx context.Context, req llm.Request) RunOutcome {
	return s.loop(ctx, s.inner.Run(ctx, req))
}

func (s *syncHITL) Continue(ctx context.Context, runID string, resolutions []Resolution) RunOutcome {
	return s.loop(ctx, s.inner.Continue(ctx, runID, resolutions))
}

func (s *syncHITL) loop(ctx context.Context, out RunOutcome) RunOutcome {
	for out.Status == StatusPaused {
		if s.approve == nil {
			out.Status = StatusError
			out.Err = fmt.Errorf("%w (run_id=%s)", ErrPaused, out.RunID)
			return out
		}
		resolutions := make([]Resolution, 0, len(out.Requirements))
		for _, rq := range out.Requirements {
			res, err := s.approve(ctx, rq.ToolCall)
			if err != nil {
				return outcomeError(out.RunID, out.Response, out.Messages, fmt.Errorf("agent: sync HITL for %q: %w", rq.ToolCall.Name, err))
			}
			res.ID = rq.ID
			resolutions = append(resolutions, res)
		}
		out = s.inner.Continue(ctx, out.RunID, resolutions)
	}
	return out
}
