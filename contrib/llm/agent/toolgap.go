package agent

import (
	"context"
	"fmt"

	"github.com/rushteam/beauty/contrib/llm"
)

// MissingToolResult 为没有对应结果的工具调用生成补上的正文。
// 返回 error 时本步不再调用模型。
type MissingToolResult func(ctx context.Context, call llm.ToolCall) (string, error)

func defaultMissingToolResult(_ context.Context, call llm.ToolCall) (string, error) {
	name := call.Name
	if name == "" {
		name = call.ID
	}
	return fmt.Sprintf("工具 %q 没有结果,调用已中断", name), nil
}

// FillMissingToolResults 在发给模型前补上悬空的工具调用。
// 只认紧跟在该条 assistant 消息后面的连续 tool 消息;中间插入了别的角色就视为没有结果。
// 没有 ID 的调用无法对应结果,保持原样。
func FillMissingToolResults(ctx context.Context, msgs []llm.Message, fill MissingToolResult) ([]llm.Message, error) {
	if fill == nil {
		fill = defaultMissingToolResult
	}
	out := make([]llm.Message, 0, len(msgs))
	for i, msg := range msgs {
		out = append(out, msg)
		if msg.Role != llm.Assistant || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID == "" || toolResultFollows(msgs[i+1:], tc.ID) {
				continue
			}
			content, err := fill(ctx, tc)
			if err != nil {
				return nil, err
			}
			out = append(out, llm.Message{
				Role:       llm.Tool,
				ToolCallID: tc.ID,
				Content:    content,
				Source:     llm.SourceMiddleware,
			})
		}
	}
	return out, nil
}

func toolResultFollows(msgs []llm.Message, id string) bool {
	for _, m := range msgs {
		if m.Role != llm.Tool {
			return false
		}
		if m.ToolCallID == id {
			return true
		}
	}
	return false
}
