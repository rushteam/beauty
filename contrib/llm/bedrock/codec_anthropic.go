package bedrock

import (
	"encoding/json"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/internal/anthropicwire"
)

// anthropicClaudeVersion 是 Bedrock 上 Anthropic 模型请求体里必填的 anthropic_version。
// 注意:与 api.anthropic.com 的 anthropic-version 头(2023-06-01)不同,这是 Bedrock 专用值。
const anthropicClaudeVersion = "bedrock-2023-05-31"

// anthropicCodec 是 Bedrock 上的 Anthropic Claude 家族:线格式与原生 Messages API 一致
// (复用 anthropicwire),差别仅在 body 不带 model 字段、改带 anthropic_version,且认证/流式走 Bedrock。
// 全能力:tools + 多模态 + 流式 tool_calls。
type anthropicCodec struct{}

func (anthropicCodec) Name() string { return "anthropic" }

// bedrockAnthropicReq 是 Bedrock 版 Messages 请求体:无 model,有 anthropic_version。
type bedrockAnthropicReq struct {
	AnthropicVersion string                  `json:"anthropic_version"`
	System           string                  `json:"system,omitempty"`
	Messages         []anthropicwire.Message `json:"messages"`
	MaxTokens        int                     `json:"max_tokens"`
	Temperature      float64                 `json:"temperature,omitempty"`
	StopSeqs         []string                `json:"stop_sequences,omitempty"`
	Tools            []anthropicwire.Tool    `json:"tools,omitempty"`
	ToolChoice       any                     `json:"tool_choice,omitempty"`
}

func (anthropicCodec) BuildBody(req llm.Request, _ bool) ([]byte, error) {
	return json.Marshal(bedrockAnthropicReq{
		AnthropicVersion: anthropicClaudeVersion,
		System:           req.System,
		Messages:         anthropicwire.BuildMessages(req.Messages),
		MaxTokens:        anthropicwire.ResolveMaxTokens(req.MaxTokens),
		Temperature:      req.Temperature,
		StopSeqs:         req.Stop,
		Tools:            anthropicwire.BuildTools(req.Tools),
		ToolChoice:       anthropicwire.BuildToolChoice(req.ToolChoice),
	})
}

func (anthropicCodec) ParseResponse(body []byte) (*llm.Response, error) {
	return anthropicwire.ParseResponse(body)
}

func (anthropicCodec) NewStream() StreamState { return anthropicwire.NewEventAccumulator() }
