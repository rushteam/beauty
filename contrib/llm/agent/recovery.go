package agent

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
)

// Recovery 控制同一次 run 内对模型错误的恢复,不走 Fallback(那是换 client)。
//
//   - ErrorContextOverflow / prompt_too_long: 强制压缩消息后重试
//   - ErrorMaxOutput / max_output_tokens: 提高 MaxTokens 后重试
type Recovery struct {
	// MaxAttempts 每个模型调用最多额外重试次数(不含首次)。<=0 视为 2。
	MaxAttempts int
	// MaxTokensBump 每次输出超限时 MaxTokens 增加量。<=0 视为 4096。
	MaxTokensBump int
}

// DefaultRecovery 是 Runner.Recovery 为 nil 时的默认策略。
func DefaultRecovery() Recovery {
	return Recovery{MaxAttempts: 2, MaxTokensBump: 4096}
}

func (r Recovery) attempts() int {
	if r.MaxAttempts > 0 {
		return r.MaxAttempts
	}
	return 2
}

func (r Recovery) bump() int {
	if r.MaxTokensBump > 0 {
		return r.MaxTokensBump
	}
	return 4096
}

func (r *Runner) recovery() Recovery {
	if r != nil && r.Recovery != nil {
		return *r.Recovery
	}
	return DefaultRecovery()
}

func (r *Runner) callModelRecover(ctx context.Context, req *llm.Request, step int, emit func(Event)) (*llm.Response, error) {
	resp, err := r.callModel(ctx, *req, step, emit)
	if err == nil {
		return resp, nil
	}
	rec := r.recovery()
	for i := 0; i < rec.attempts(); i++ {
		kind := llm.ClassifyError(err)
		switch kind {
		case llm.ErrorMaxOutput:
			if req.MaxTokens <= 0 {
				req.MaxTokens = rec.bump()
			} else {
				req.MaxTokens += rec.bump()
			}
		case llm.ErrorContextOverflow:
			compacted, cerr := r.forceCompact(ctx, req.Messages)
			if cerr != nil {
				return nil, err
			}
			req.Messages = compacted
		default:
			return nil, err
		}
		resp, err = r.callModel(ctx, *req, step, emit)
		if err == nil {
			return resp, nil
		}
	}
	return nil, err
}

func (r *Runner) forceCompact(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	aggressive := compaction.Chain(
		compaction.DefaultSnip(),
		&compaction.Microcompact{KeepRecent: 2},
	)
	out, err := aggressive.Compact(ctx, msgs)
	if err != nil {
		return nil, err
	}
	if r.Compaction != nil {
		more, err := r.Compaction.Compact(ctx, out)
		if err != nil {
			return out, nil // 已有一轮压缩,摘要失败不阻断恢复
		}
		return more, nil
	}
	return out, nil
}
