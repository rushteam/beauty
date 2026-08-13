package agent

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// MergeHooks 合并两个 Hooks,依次调用 a 然后 b 的回调。任一返回 error 即中止。
// BeforeTool 链中如果 a 返回非 PermitAllow 的 Permission,则 b 不再执行。
func MergeHooks(a, b Hooks) Hooks {
	return Hooks{
		BeforeTurn:  chainTurnHook(a.BeforeTurn, b.BeforeTurn),
		AfterTurn:   chainAfterTurn(a.AfterTurn, b.AfterTurn),
		BeforeModel: chainBeforeModel(a.BeforeModel, b.BeforeModel),
		AfterModel:  chainAfterModel(a.AfterModel, b.AfterModel),
		OnChunk:     chainOnChunk(a.OnChunk, b.OnChunk),
		BeforeTool:  chainBeforeTool(a.BeforeTool, b.BeforeTool),
		AfterTool:   chainAfterTool(a.AfterTool, b.AfterTool),
	}
}

func chainTurnHook(
	a, b func(context.Context, *llm.Request) error,
) func(context.Context, *llm.Request) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context, req *llm.Request) error {
		if err := a(ctx, req); err != nil {
			return err
		}
		return b(ctx, req)
	}
}

func chainAfterTurn(
	a, b func(context.Context, *RunOutcome),
) func(context.Context, *RunOutcome) {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context, out *RunOutcome) {
		a(ctx, out)
		b(ctx, out)
	}
}

func chainBeforeModel(
	a, b func(context.Context, int, *llm.Request) error,
) func(context.Context, int, *llm.Request) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context, step int, req *llm.Request) error {
		if err := a(ctx, step, req); err != nil {
			return err
		}
		return b(ctx, step, req)
	}
}

func chainAfterModel(
	a, b func(context.Context, int, *llm.Response) error,
) func(context.Context, int, *llm.Response) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context, step int, resp *llm.Response) error {
		if err := a(ctx, step, resp); err != nil {
			return err
		}
		return b(ctx, step, resp)
	}
}

func chainOnChunk(
	a, b func(context.Context, int, *llm.Chunk) error,
) func(context.Context, int, *llm.Chunk) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context, step int, c *llm.Chunk) error {
		if err := a(ctx, step, c); err != nil {
			return err
		}
		return b(ctx, step, c)
	}
}

func chainBeforeTool(
	a, b func(context.Context, int, *llm.ToolCall) (Permission, error),
) func(context.Context, int, *llm.ToolCall) (Permission, error) {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context, step int, tc *llm.ToolCall) (Permission, error) {
		perm, err := a(ctx, step, tc)
		if err != nil {
			return perm, err
		}
		if perm != PermitAllow {
			return perm, nil
		}
		return b(ctx, step, tc)
	}
}

func chainAfterTool(
	a, b func(context.Context, int, llm.ToolCall, *string) error,
) func(context.Context, int, llm.ToolCall, *string) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context, step int, tc llm.ToolCall, result *string) error {
		if err := a(ctx, step, tc, result); err != nil {
			return err
		}
		return b(ctx, step, tc, result)
	}
}
