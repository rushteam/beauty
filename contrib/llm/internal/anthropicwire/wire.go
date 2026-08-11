// Package anthropicwire 是 Anthropic Messages API 的线格式翻译层:把 beauty 中立的
// llm.Message/ToolDef/Response 与 Anthropic 的 content-block 线格式互转。纯逻辑、不含传输,
// 供 llm/anthropic(HTTP 直连 api.anthropic.com)与 llm/bedrock(Bedrock 上的 Claude)复用。
//
// 两个 provider 只在传输层不同(认证、URL、流式帧编码),消息/工具/响应/流式事件的语义完全一致,
// 故集中在此,避免两份实现漂移。纯标准库。
package anthropicwire

import (
	"encoding/json"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// DefaultMaxTokens 是 Anthropic 要求 max_tokens 必填时的兜底值。
const DefaultMaxTokens = 1024

// Message 是 Anthropic 的一条消息。Content 既可是纯文本字符串,也可是 content block 数组
// (工具往返 / 多模态时用后者)。
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// CacheControl 是 Anthropic prompt caching 的标记。
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// Block 是 Anthropic 的 content block:text / image / tool_use / tool_result / thinking。
type Block struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`          // type=text
	Thinking     string          `json:"thinking,omitempty"`      // type=thinking
	ID           string          `json:"id,omitempty"`            // type=tool_use
	Name         string          `json:"name,omitempty"`          // type=tool_use
	Input        json.RawMessage `json:"input,omitempty"`         // type=tool_use
	ToolUseID    string          `json:"tool_use_id,omitempty"`   // type=tool_result
	Content      string          `json:"content,omitempty"`       // type=tool_result
	Source       *Source         `json:"source,omitempty"`        // type=image
	CacheControl *CacheControl   `json:"cache_control,omitempty"` // prompt caching
}

// Source 是 image block 的图片来源(base64 或 url)。
type Source struct {
	Type      string `json:"type"`       // "base64" / "url"
	MediaType string `json:"media_type"` // "image/png" etc
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// Tool 是 Anthropic 的工具声明。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// BuildMessages 把中立消息翻译成 Anthropic 消息:tool 结果并入一个 user 回合(多 tool_result 块),
// 带 ToolCalls 的 assistant 回合转成 text + tool_use 块;多模态消息用 content block 数组。
// 如果消息设置了 CacheControl,会在最后一个 block 上附加 cache_control 标记。
func BuildMessages(msgs []llm.Message) []Message {
	out := make([]Message, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		cc := resolveCacheControl(m.CacheControl)
		switch {
		case m.Role == llm.Tool:
			blocks := []Block{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}}
			for i+1 < len(msgs) && msgs[i+1].Role == llm.Tool {
				i++
				blocks = append(blocks, Block{Type: "tool_result", ToolUseID: msgs[i].ToolCallID, Content: msgs[i].Content})
			}
			if cc != nil {
				blocks[len(blocks)-1].CacheControl = cc
			}
			out = append(out, Message{Role: "user", Content: blocks})
		case m.Role == llm.Assistant && len(m.ToolCalls) > 0:
			var blocks []Block
			if m.Content != "" {
				blocks = append(blocks, Block{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, Block{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			if cc != nil && len(blocks) > 0 {
				blocks[len(blocks)-1].CacheControl = cc
			}
			out = append(out, Message{Role: string(m.Role), Content: blocks})
		case len(m.Parts) > 0:
			blocks := BuildParts(m.Parts)
			if cc != nil && len(blocks) > 0 {
				blocks[len(blocks)-1].CacheControl = cc
			}
			out = append(out, Message{Role: string(m.Role), Content: blocks})
		default:
			if cc != nil {
				blocks := []Block{{Type: "text", Text: m.Content, CacheControl: cc}}
				out = append(out, Message{Role: string(m.Role), Content: blocks})
			} else {
				out = append(out, Message{Role: string(m.Role), Content: m.Content})
			}
		}
	}
	return out
}

func resolveCacheControl(cc string) *CacheControl {
	if cc == "" {
		return nil
	}
	return &CacheControl{Type: cc}
}

// BuildSystem 构建 Anthropic 的 system 字段。当 cacheControl 非空时,返回带 cache_control
// 标记的 content block 数组;否则返回纯字符串。
func BuildSystem(system, cacheControl string) any {
	if system == "" {
		return nil
	}
	if cacheControl == "" {
		return system
	}
	return []Block{{
		Type:         "text",
		Text:         system,
		CacheControl: &CacheControl{Type: cacheControl},
	}}
}

// BuildParts 把多模态 Part(文本/图片)翻译成 content block 数组。
func BuildParts(parts []llm.Part) []Block {
	blocks := make([]Block, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case llm.PartText:
			blocks = append(blocks, Block{Type: "text", Text: p.Text})
		case llm.PartImage:
			blk := Block{Type: "image"}
			if strings.HasPrefix(p.ImageURL, "data:") {
				mt, data := parseDataURI(p.ImageURL)
				blk.Source = &Source{Type: "base64", MediaType: mt, Data: data}
			} else {
				blk.Source = &Source{Type: "url", URL: p.ImageURL}
			}
			blocks = append(blocks, blk)
		}
	}
	return blocks
}

func parseDataURI(uri string) (mediaType, data string) {
	after, _ := strings.CutPrefix(uri, "data:")
	parts := strings.SplitN(after, ",", 2)
	if len(parts) != 2 {
		return "application/octet-stream", after
	}
	mt := strings.TrimSuffix(parts[0], ";base64")
	return mt, parts[1]
}

// BuildTools 把中立工具声明翻译成 Anthropic 工具(input_schema 必填,空则给空对象 schema)。
func BuildTools(defs []llm.ToolDef) []Tool {
	if len(defs) == 0 {
		return nil
	}
	ts := make([]Tool, len(defs))
	for i, d := range defs {
		schema := d.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		ts[i] = Tool{Name: d.Name, Description: d.Description, InputSchema: schema}
	}
	return ts
}

// BuildToolChoice 映射中立 ToolChoice 到 Anthropic 的 tool_choice 对象(nil 表示不带该字段)。
func BuildToolChoice(tc string) any {
	switch tc {
	case "":
		return nil
	case "auto":
		return map[string]string{"type": "auto"}
	case "none":
		return map[string]string{"type": "none"}
	case "required":
		return map[string]string{"type": "any"} // Anthropic 用 "any" 表示"必须调用某个"
	default:
		return map[string]string{"type": "tool", "name": tc}
	}
}

// ResolveMaxTokens 返回有效 max_tokens(<=0 时用 DefaultMaxTokens,因 Anthropic 要求必填)。
func ResolveMaxTokens(v int) int {
	if v <= 0 {
		return DefaultMaxTokens
	}
	return v
}

// ParseResponse 解析 Anthropic Messages 的非流式响应体为中立 llm.Response。
// 支持 thinking content blocks 和 prompt caching usage 字段。
func ParseResponse(body []byte) (*llm.Response, error) {
	var out struct {
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	r := &llm.Response{
		Model:      out.Model,
		StopReason: out.StopReason,
		Usage: llm.Usage{
			InputTokens:              out.Usage.InputTokens,
			OutputTokens:             out.Usage.OutputTokens,
			CacheCreationInputTokens: out.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     out.Usage.CacheReadInputTokens,
		},
	}
	var textSB, thinkingSB strings.Builder
	for _, blk := range out.Content {
		switch blk.Type {
		case "text":
			textSB.WriteString(blk.Text)
		case "thinking":
			thinkingSB.WriteString(blk.Thinking)
		case "tool_use":
			r.ToolCalls = append(r.ToolCalls, llm.ToolCall{ID: blk.ID, Name: blk.Name, Arguments: blk.Input})
		}
	}
	r.Content = textSB.String()
	r.Thinking = thinkingSB.String()
	return r, nil
}

// StreamDelta 是 EventAccumulator.Feed 返回的增量结果。
type StreamDelta struct {
	Text     string // 文本增量
	Thinking string // 思考增量
	Done     bool   // 是否流结束
}

// EventAccumulator 是 Anthropic 流式事件的状态机:逐个吃 SSE 事件 JSON,组装文本增量、
// 思考增量、流式 tool_use(input_json_delta 拼 JSON)与 usage。传输层(SSE 行 / Bedrock
// event-stream 帧)各自把事件 JSON 喂进来,channel/ctx 由调用方掌控。
//
// 事件类型:message_start / content_block_start / content_block_delta /
// content_block_stop / message_delta / message_stop。
type EventAccumulator struct {
	usage      llm.Usage
	tools      []*toolAcc
	blockMap   map[int]*toolAcc
	thinkingSB strings.Builder
}

type toolAcc struct {
	id, name string
	args     strings.Builder
}

// NewEventAccumulator 新建累加器。
func NewEventAccumulator() *EventAccumulator {
	return &EventAccumulator{blockMap: map[int]*toolAcc{}}
}

// Feed 处理一个事件 JSON,返回增量结果。无法解析的事件被安静跳过。
//
// 向后兼容:仍提供 (delta string, done bool) 签名的旧方法作为 FeedText。
func (a *EventAccumulator) Feed(data []byte) (delta string, done bool) {
	sd := a.FeedExt(data)
	return sd.Text, sd.Done
}

// FeedExt 处理一个事件 JSON,返回完整的增量结果(文本 + 思考)。
func (a *EventAccumulator) FeedExt(data []byte) StreamDelta {
	var ev struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock *struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Name  string `json:"name"`
			Text  string `json:"text"`
			Input any    `json:"input"`
		} `json:"content_block"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		Usage *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		Message *struct {
			Usage *struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return StreamDelta{}
	}

	switch ev.Type {
	case "message_start":
		if ev.Message != nil && ev.Message.Usage != nil {
			a.usage.InputTokens = ev.Message.Usage.InputTokens
			a.usage.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
			a.usage.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
		} else if ev.Usage != nil {
			a.usage.InputTokens = ev.Usage.InputTokens
			a.usage.CacheCreationInputTokens = ev.Usage.CacheCreationInputTokens
			a.usage.CacheReadInputTokens = ev.Usage.CacheReadInputTokens
		}
	case "content_block_start":
		if ev.ContentBlock == nil {
			return StreamDelta{}
		}
		switch ev.ContentBlock.Type {
		case "tool_use":
			acc := &toolAcc{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			a.tools = append(a.tools, acc)
			a.blockMap[ev.Index] = acc
		case "thinking":
			// thinking block 的增量通过 delta.type="thinking_delta" 自动识别，无需额外标记
		}
	case "content_block_delta":
		if ev.Delta == nil {
			return StreamDelta{}
		}
		switch ev.Delta.Type {
		case "text_delta", "":
			return StreamDelta{Text: ev.Delta.Text}
		case "thinking_delta":
			a.thinkingSB.WriteString(ev.Delta.Thinking)
			return StreamDelta{Thinking: ev.Delta.Thinking}
		case "input_json_delta":
			if acc, ok := a.blockMap[ev.Index]; ok {
				acc.args.WriteString(ev.Delta.PartialJSON)
			}
		}
	case "message_delta":
		if ev.Usage != nil {
			a.usage.OutputTokens = ev.Usage.OutputTokens
		}
	case "message_stop":
		return StreamDelta{Done: true}
	}
	return StreamDelta{}
}

// Result 返回累积好的 tool_calls 与 usage(通常在 done 后调用)。
func (a *EventAccumulator) Result() ([]llm.ToolCall, llm.Usage) {
	var tcs []llm.ToolCall
	for _, acc := range a.tools {
		args := acc.args.String()
		if args == "" {
			args = "{}"
		}
		tcs = append(tcs, llm.ToolCall{ID: acc.id, Name: acc.name, Arguments: json.RawMessage(args)})
	}
	return tcs, a.usage
}

// ThinkingText 返回累积的完整思考文本。
func (a *EventAccumulator) ThinkingText() string {
	return a.thinkingSB.String()
}
