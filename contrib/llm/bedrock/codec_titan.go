package bedrock

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// titanCodec 是 Amazon Titan 家族:Titan Text(文本生成)+ Titan Embeddings(向量)。
// 纯文本,无 tools/多模态;多轮消息用 "Role: text" 逐行拼成单 prompt(system 提到最前)。
type titanCodec struct{}

func (titanCodec) Name() string { return "titan" }

var _ EmbedCodec = titanCodec{}

// titanTextReq 是 Titan Text 的 invoke 请求体。
type titanTextReq struct {
	InputText string          `json:"inputText"`
	Config    titanTextConfig `json:"textGenerationConfig"`
}

type titanTextConfig struct {
	MaxTokenCount int      `json:"maxTokenCount,omitempty"`
	Temperature   float64  `json:"temperature,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

func (titanCodec) BuildBody(req llm.Request, _ bool) ([]byte, error) {
	return json.Marshal(titanTextReq{
		InputText: titanPrompt(req),
		Config: titanTextConfig{
			MaxTokenCount: req.MaxTokens,
			Temperature:   req.Temperature,
			StopSequences: req.Stop,
		},
	})
}

// titanPrompt 把 system + 多轮消息拼成 Titan 的单段 prompt。Titan 无专门 chat 模板,
// 采用 "User: ... \nBot: ..." 约定并以 "Bot:" 收尾提示续写。
func titanPrompt(req llm.Request) string {
	var sb strings.Builder
	if req.System != "" {
		sb.WriteString(req.System)
		sb.WriteString("\n\n")
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.System:
			sb.WriteString(firstText(m))
			sb.WriteString("\n\n")
		case llm.Assistant:
			sb.WriteString("Bot: ")
			sb.WriteString(firstText(m))
			sb.WriteString("\n")
		default: // user / tool
			sb.WriteString("User: ")
			sb.WriteString(firstText(m))
			sb.WriteString("\n")
		}
	}
	sb.WriteString("Bot:")
	return sb.String()
}

func (titanCodec) ParseResponse(body []byte) (*llm.Response, error) {
	var out struct {
		InputTextTokenCount int `json:"inputTextTokenCount"`
		Results             []struct {
			OutputText       string `json:"outputText"`
			TokenCount       int    `json:"tokenCount"`
			CompletionReason string `json:"completionReason"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	r := &llm.Response{Usage: llm.Usage{InputTokens: out.InputTextTokenCount}}
	if len(out.Results) > 0 {
		r.Content = strings.TrimSpace(out.Results[0].OutputText)
		r.StopReason = out.Results[0].CompletionReason
		r.Usage.OutputTokens = out.Results[0].TokenCount
	}
	return r, nil
}

func (titanCodec) NewStream() StreamState { return &titanStream{} }

// titanStream 累加 Titan 的流式 chunk:{"outputText":"...","totalOutputTextTokenCount":N,"completionReason":...}。
type titanStream struct {
	usage llm.Usage
}

func (s *titanStream) Feed(event []byte) (string, bool) {
	var ev struct {
		OutputText                string `json:"outputText"`
		TotalOutputTextTokenCount int    `json:"totalOutputTextTokenCount"`
		InputTextTokenCount       int    `json:"inputTextTokenCount"`
		CompletionReason          string `json:"completionReason"`
	}
	if json.Unmarshal(event, &ev) != nil {
		return "", false
	}
	if ev.InputTextTokenCount > 0 {
		s.usage.InputTokens = ev.InputTextTokenCount
	}
	if ev.TotalOutputTextTokenCount > 0 {
		s.usage.OutputTokens = ev.TotalOutputTextTokenCount
	}
	return ev.OutputText, ev.CompletionReason != ""
}

func (s *titanStream) Result() ([]llm.ToolCall, llm.Usage) { return nil, s.usage }

// --- Titan Embeddings ---

func (titanCodec) BuildEmbedBody(text string) ([]byte, error) {
	return json.Marshal(struct {
		InputText string `json:"inputText"`
	}{InputText: text})
}

func (titanCodec) ParseEmbedResponse(body []byte) ([]float32, error) {
	var out struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Embedding) == 0 {
		return nil, errors.New("bedrock: titan: empty embedding in response")
	}
	return out.Embedding, nil
}
