package compaction

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// BeforeModelHook 把 Strategy 适配为 agent Hooks.BeforeModel 回调。
func BeforeModelHook(s Strategy) func(ctx context.Context, step int, req *llm.Request) error {
	if s == nil {
		return nil
	}
	return func(ctx context.Context, _ int, req *llm.Request) error {
		out, err := s.Compact(ctx, req.Messages)
		if err != nil {
			return err
		}
		req.Messages = out
		return nil
	}
}
