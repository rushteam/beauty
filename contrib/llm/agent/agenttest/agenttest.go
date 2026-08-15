// Package agenttest 提供 agent 测试工具:按轮次编排 LLM 响应的 mock builder。
package agenttest

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// Turn 是一个模型调用轮次的编排:包含可选回调和预设响应序列。
type Turn struct {
	Callbacks []func(ctx context.Context, req llm.Request)
	Response  *llm.Response
	Err       error
}

// ResponseBuilder 用于编排多轮 LLM 响应。
type ResponseBuilder struct {
	turns []Turn
}

// NewResponseBuilder 创建 builder,可选设置第一轮的回调。
func NewResponseBuilder(callbacks ...func(ctx context.Context, req llm.Request)) *ResponseBuilder {
	return &ResponseBuilder{
		turns: []Turn{{Callbacks: callbacks}},
	}
}

// AddResponse 为当前轮设置预设响应。
func (b *ResponseBuilder) AddResponse(resp *llm.Response) *ResponseBuilder {
	t := b.current()
	t.Response = resp
	return b
}

// AddText 为当前轮设置纯文本响应。
func (b *ResponseBuilder) AddText(text string) *ResponseBuilder {
	t := b.current()
	resp := t.ensureResponse()
	resp.Content = text
	return b
}

// AddToolCall 为当前轮添加工具调用响应。
func (b *ResponseBuilder) AddToolCall(id, name, args string) *ResponseBuilder {
	t := b.current()
	resp := t.ensureResponse()
	resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
		ID:        id,
		Name:      name,
		Arguments: json.RawMessage(args),
	})
	return b
}

// AddError 为当前轮设置错误响应。
func (b *ResponseBuilder) AddError(err error) *ResponseBuilder {
	b.current().Err = err
	return b
}

// NewTurn 开始新一轮,可选设置回调。
func (b *ResponseBuilder) NewTurn(callbacks ...func(ctx context.Context, req llm.Request)) *ResponseBuilder {
	b.turns = append(b.turns, Turn{Callbacks: callbacks})
	return b
}

// Build 返回完成的 Turn 序列。
func (b *ResponseBuilder) Build() []Turn {
	out := make([]Turn, len(b.turns))
	copy(out, b.turns)
	return out
}

func (b *ResponseBuilder) current() *Turn {
	return &b.turns[len(b.turns)-1]
}

func (t *Turn) ensureResponse() *llm.Response {
	if t.Response == nil {
		t.Response = &llm.Response{}
	}
	return t.Response
}

// ScriptedClient 是按 Turn 序列返回预设响应的 llm.Client。
// 可直接用于 agent.Runner{Client: client}。
type ScriptedClient struct {
	turns       []Turn
	currentTurn int
}

// NewScriptedClient 创建按轮次编排的 mock client。
func NewScriptedClient(turns []Turn) *ScriptedClient {
	return &ScriptedClient{turns: turns}
}

func (c *ScriptedClient) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if c.currentTurn >= len(c.turns) {
		panic(fmt.Sprintf("agenttest: no more scripted turns (turn %d)", c.currentTurn))
	}
	turn := c.turns[c.currentTurn]
	c.currentTurn++
	for _, cb := range turn.Callbacks {
		cb(ctx, req)
	}
	if turn.Err != nil {
		return nil, turn.Err
	}
	return turn.Response, nil
}

func (c *ScriptedClient) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		resp, err := c.Generate(ctx, req)
		if err != nil {
			yield(llm.Chunk{}, err)
			return
		}
		if resp == nil {
			yield(llm.Chunk{}, nil)
			return
		}
		yield(llm.Chunk{
			Delta:     resp.Content,
			ToolCalls: resp.ToolCalls,
			Thinking:  resp.Thinking,
		}, nil)
	}
}

// CurrentTurn 返回当前轮次(0-based)。
func (c *ScriptedClient) CurrentTurn() int {
	return c.currentTurn
}

// NewRunner 创建一个带 scripted client 的 Runner,方便测试。
func NewRunner(turns []Turn, tools ...agent.Tool) *agent.Runner {
	return &agent.Runner{
		Client: NewScriptedClient(turns),
		Tools:  tools,
		Name:   "test",
	}
}
