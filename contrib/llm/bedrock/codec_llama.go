package bedrock

import (
	"encoding/json"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// llamaCodec 是 Meta Llama 家族(Llama 3/3.1/3.2 instruct)。纯文本,无 tools;
// 多轮消息按 Llama 3 官方 chat 模板拼成单 prompt。
type llamaCodec struct{}

func (llamaCodec) Name() string { return "llama" }

// llamaReq 是 Llama 的 invoke 请求体。
type llamaReq struct {
	Prompt      string  `json:"prompt"`
	MaxGenLen   int     `json:"max_gen_len,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
}

func (llamaCodec) BuildBody(req llm.Request, _ bool) ([]byte, error) {
	return json.Marshal(llamaReq{
		Prompt:      llamaPrompt(req),
		MaxGenLen:   req.MaxTokens,
		Temperature: req.Temperature,
	})
}

// llamaPrompt 按 Llama 3 模板拼:每个回合用
// <|start_header_id|>role<|end_header_id|>\n\n{content}<|eot_id|>,整体以 <|begin_of_text|> 开头、
// 以一个空的 assistant header 结尾提示模型续写。
func llamaPrompt(req llm.Request) string {
	var sb strings.Builder
	sb.WriteString("<|begin_of_text|>")
	writeTurn := func(role, content string) {
		sb.WriteString("<|start_header_id|>")
		sb.WriteString(role)
		sb.WriteString("<|end_header_id|>\n\n")
		sb.WriteString(content)
		sb.WriteString("<|eot_id|>")
	}
	if req.System != "" {
		writeTurn("system", req.System)
	}
	for _, m := range req.Messages {
		role := "user"
		switch m.Role {
		case llm.System:
			role = "system"
		case llm.Assistant:
			role = "assistant"
		}
		writeTurn(role, firstText(m))
	}
	sb.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")
	return sb.String()
}

func (llamaCodec) ParseResponse(body []byte) (*llm.Response, error) {
	var out struct {
		Generation           string `json:"generation"`
		PromptTokenCount     int    `json:"prompt_token_count"`
		GenerationTokenCount int    `json:"generation_token_count"`
		StopReason           string `json:"stop_reason"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &llm.Response{
		Content:    strings.TrimSpace(out.Generation),
		StopReason: out.StopReason,
		Usage:      llm.Usage{InputTokens: out.PromptTokenCount, OutputTokens: out.GenerationTokenCount},
	}, nil
}

func (llamaCodec) NewStream() StreamState { return &llamaStream{} }

// llamaStream 累加 Llama 流式 chunk:{"generation":"...","generation_token_count":N,"stop_reason":...}。
type llamaStream struct {
	usage llm.Usage
}

func (s *llamaStream) Feed(event []byte) (string, bool) {
	var ev struct {
		Generation           string `json:"generation"`
		PromptTokenCount     int    `json:"prompt_token_count"`
		GenerationTokenCount int    `json:"generation_token_count"`
		StopReason           string `json:"stop_reason"`
	}
	if json.Unmarshal(event, &ev) != nil {
		return "", false
	}
	if ev.PromptTokenCount > 0 {
		s.usage.InputTokens = ev.PromptTokenCount
	}
	if ev.GenerationTokenCount > 0 {
		s.usage.OutputTokens = ev.GenerationTokenCount
	}
	return ev.Generation, ev.StopReason != ""
}

func (s *llamaStream) Result() ([]llm.ToolCall, llm.Usage) { return nil, s.usage }
