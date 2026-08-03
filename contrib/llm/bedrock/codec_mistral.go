package bedrock

import (
	"encoding/json"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// mistralCodec 是 Mistral 家族(mistral / mixtral instruct)。纯文本,无 tools;
// 多轮消息按 Mistral 的 [INST] 模板拼成单 prompt(Mistral 无独立 system 角色,system 并入首个用户轮)。
type mistralCodec struct{}

func (mistralCodec) Name() string { return "mistral" }

// mistralReq 是 Mistral 的 invoke 请求体。
type mistralReq struct {
	Prompt      string   `json:"prompt"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature float64  `json:"temperature,omitempty"`
	TopP        float64  `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

func (mistralCodec) BuildBody(req llm.Request, _ bool) ([]byte, error) {
	return json.Marshal(mistralReq{
		Prompt:      mistralPrompt(req),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stop:        req.Stop,
	})
}

// mistralPrompt 按 Mistral 模板拼:<s>[INST] {user} [/INST] {assistant}</s> 交替;
// system 文本前置到第一个 user 轮内(Mistral 无 system 角色)。
func mistralPrompt(req llm.Request) string {
	var sb strings.Builder
	sb.WriteString("<s>")
	pendingSystem := req.System
	firstUser := true
	for _, m := range req.Messages {
		switch m.Role {
		case llm.Assistant:
			sb.WriteString(" ")
			sb.WriteString(firstText(m))
			sb.WriteString("</s>")
		case llm.System:
			// 累积到下一个 user 轮
			if pendingSystem != "" {
				pendingSystem += "\n\n"
			}
			pendingSystem += firstText(m)
		default: // user / tool
			content := firstText(m)
			if pendingSystem != "" {
				content = pendingSystem + "\n\n" + content
				pendingSystem = ""
			}
			if !firstUser {
				sb.WriteString("<s>")
			}
			sb.WriteString("[INST] ")
			sb.WriteString(content)
			sb.WriteString(" [/INST]")
			firstUser = false
		}
	}
	// 若只有 system 没有 user,兜底把 system 作为一轮指令
	if pendingSystem != "" {
		sb.WriteString("[INST] ")
		sb.WriteString(pendingSystem)
		sb.WriteString(" [/INST]")
	}
	return sb.String()
}

func (mistralCodec) ParseResponse(body []byte) (*llm.Response, error) {
	var out struct {
		Outputs []struct {
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	r := &llm.Response{}
	if len(out.Outputs) > 0 {
		r.Content = strings.TrimSpace(out.Outputs[0].Text)
		r.StopReason = out.Outputs[0].StopReason
	}
	return r, nil
}

func (mistralCodec) NewStream() StreamState { return &mistralStream{} }

// mistralStream 累加 Mistral 流式 chunk:{"outputs":[{"text":"...","stop_reason":...}]}。
type mistralStream struct{}

func (s *mistralStream) Feed(event []byte) (string, bool) {
	var ev struct {
		Outputs []struct {
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"outputs"`
	}
	if json.Unmarshal(event, &ev) != nil || len(ev.Outputs) == 0 {
		return "", false
	}
	return ev.Outputs[0].Text, ev.Outputs[0].StopReason != ""
}

func (s *mistralStream) Result() ([]llm.ToolCall, llm.Usage) { return nil, llm.Usage{} }
