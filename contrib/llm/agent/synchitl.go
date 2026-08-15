package agent

import (
	"context"
	"fmt"
	"iter"

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

func (s *syncHITL) Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
	return s.loopIter(ctx, s.inner.Run(ctx, req, opts...))
}

func (s *syncHITL) Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error] {
	return s.loopIter(ctx, s.inner.Continue(ctx, runID, resolutions, opts...))
}

func (s *syncHITL) loopIter(ctx context.Context, seq iter.Seq2[Event, error]) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		for {
			out := CollectOutcome(seq)
			switch out.Status {
			case StatusDone:
				yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID}, nil)
				return
			case StatusError:
				yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err}, out.Err)
				return
			case StatusPaused:
				if s.approve == nil {
					err := fmt.Errorf("%w (run_id=%s)", ErrPaused, out.RunID)
					yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: err}, err)
					return
				}
				resolutions := make([]Resolution, 0, len(out.Requirements))
				for _, rq := range out.Requirements {
					res, err := s.approve(ctx, rq.ToolCall)
					if err != nil {
						err = fmt.Errorf("agent: sync HITL for %q: %w", rq.ToolCall.Name, err)
						yield(Event{}, err)
						return
					}
					res.ID = rq.ID
					resolutions = append(resolutions, res)
				}
				seq = s.inner.Continue(ctx, out.RunID, resolutions)
			default:
				yield(Event{}, fmt.Errorf("agent: unexpected status %q", out.Status))
				return
			}
		}
	}
}
