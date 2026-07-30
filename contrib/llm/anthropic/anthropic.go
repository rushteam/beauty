// Package anthropic 是 llm.Client 的 Anthropic Messages API 实现,HTTP 直连 /v1/messages。
// BaseURL 可覆盖(网关/测试打桩)。纯标准库。
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

const (
	defaultBaseURL = "https://api.anthropic.com/v1"
	apiVersion     = "2023-06-01"
	defaultMaxTok  = 1024
)

// Client 实现 llm.Client。
type Client struct {
	apiKey  string
	baseURL string
	version string
	hc      *http.Client
}

// Option 配置 Client。
type Option func(*Client)

// WithBaseURL 覆盖 API 基地址(默认 https://api.anthropic.com/v1)。
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient 使用自定义 *http.Client。
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithVersion 覆盖 anthropic-version 头(默认 2023-06-01)。
func WithVersion(v string) Option { return func(c *Client) { c.version = v } }

// New 创建 Anthropic 客户端。
func New(apiKey string, opts ...Option) *Client {
	c := &Client{apiKey: apiKey, baseURL: defaultBaseURL, version: apiVersion, hc: http.DefaultClient}
	for _, o := range opts {
		o(c)
	}
	return c
}

var _ llm.Client = (*Client)(nil)

type messagesReq struct {
	Model       string       `json:"model"`
	System      string       `json:"system,omitempty"`
	Messages    []antMessage `json:"messages"`
	MaxTokens   int          `json:"max_tokens"`
	Temperature float64      `json:"temperature,omitempty"`
	StopSeqs    []string     `json:"stop_sequences,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
	Tools       []antTool    `json:"tools,omitempty"`
	ToolChoice  any          `json:"tool_choice,omitempty"`
}

// antMessage 的 Content 既可是纯文本字符串,也可是 content block 数组(工具往返时用后者)。
type antMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// antBlock 是 Anthropic 的 content block:text / image / tool_use / tool_result。
type antBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`        // type=text
	ID        string          `json:"id,omitempty"`          // type=tool_use
	Name      string          `json:"name,omitempty"`        // type=tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // type=tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // type=tool_result
	Content   string          `json:"content,omitempty"`     // type=tool_result
	Source    *antSource      `json:"source,omitempty"`      // type=image
}

type antSource struct {
	Type      string `json:"type"`       // "base64" / "url"
	MediaType string `json:"media_type"` // "image/png" etc
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type antTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// buildMessages 把中立消息翻译成 Anthropic 消息:tool 结果并入一个 user 回合(多 tool_result 块),
// 带 ToolCalls 的 assistant 回合转成 text + tool_use 块;多模态消息用 content block 数组。
func buildMessages(msgs []llm.Message) []antMessage {
	out := make([]antMessage, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch {
		case m.Role == llm.Tool:
			blocks := []antBlock{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}}
			for i+1 < len(msgs) && msgs[i+1].Role == llm.Tool {
				i++
				blocks = append(blocks, antBlock{Type: "tool_result", ToolUseID: msgs[i].ToolCallID, Content: msgs[i].Content})
			}
			out = append(out, antMessage{Role: "user", Content: blocks})
		case m.Role == llm.Assistant && len(m.ToolCalls) > 0:
			var blocks []antBlock
			if m.Content != "" {
				blocks = append(blocks, antBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, antBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			out = append(out, antMessage{Role: string(m.Role), Content: blocks})
		case len(m.Parts) > 0:
			out = append(out, antMessage{Role: string(m.Role), Content: buildParts(m.Parts)})
		default:
			out = append(out, antMessage{Role: string(m.Role), Content: m.Content})
		}
	}
	return out
}

func buildParts(parts []llm.Part) []antBlock {
	blocks := make([]antBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case llm.PartText:
			blocks = append(blocks, antBlock{Type: "text", Text: p.Text})
		case llm.PartImage:
			blk := antBlock{Type: "image"}
			if strings.HasPrefix(p.ImageURL, "data:") {
				mt, data := parseDataURI(p.ImageURL)
				blk.Source = &antSource{Type: "base64", MediaType: mt, Data: data}
			} else {
				blk.Source = &antSource{Type: "url", URL: p.ImageURL}
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

func buildTools(defs []llm.ToolDef) []antTool {
	if len(defs) == 0 {
		return nil
	}
	ts := make([]antTool, len(defs))
	for i, d := range defs {
		schema := d.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`) // Anthropic 要求 input_schema 必填
		}
		ts[i] = antTool{Name: d.Name, Description: d.Description, InputSchema: schema}
	}
	return ts
}

// buildToolChoice 映射中立 ToolChoice 到 Anthropic 的 tool_choice 对象。
func buildToolChoice(tc string) any {
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

func (c *Client) build(req llm.Request, stream bool) messagesReq {
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTok // Anthropic 要求 max_tokens 必填
	}
	return messagesReq{
		Model: req.Model, System: req.System, Messages: buildMessages(req.Messages),
		MaxTokens: maxTok, Temperature: req.Temperature, StopSeqs: req.Stop, Stream: stream,
		Tools: buildTools(req.Tools), ToolChoice: buildToolChoice(req.ToolChoice),
	}
}

func (c *Client) post(ctx context.Context, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", c.version)
	return c.hc.Do(httpReq)
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("anthropic: status %s: %s", resp.Status, bytes.TrimSpace(b))
}

// Generate 实现 llm.Client。
func (c *Client) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	resp, err := c.post(ctx, c.build(req, false))
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	var out struct {
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("anthropic: decode: %w", err)
	}
	r := &llm.Response{
		Model:      out.Model,
		StopReason: out.StopReason,
		Usage:      llm.Usage{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens},
	}
	var sb strings.Builder
	for _, blk := range out.Content {
		switch blk.Type {
		case "text":
			sb.WriteString(blk.Text)
		case "tool_use":
			r.ToolCalls = append(r.ToolCalls, llm.ToolCall{ID: blk.ID, Name: blk.Name, Arguments: blk.Input})
		}
	}
	r.Content = sb.String()
	return r, nil
}

// Stream 实现 llm.Client(SSE 事件流,支持文本增量与流式 tool_calls 组装)。
// 事件:content_block_start / content_block_delta / content_block_stop / message_delta / message_stop。
func (c *Client) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	resp, err := c.post(ctx, c.build(req, true))
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, apiError(resp)
	}
	out := make(chan llm.Chunk)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		type toolAcc struct {
			id, name string
			args     strings.Builder
		}
		var (
			usage    llm.Usage
			tools    []*toolAcc
			curBlock int
			blockMap = map[int]*toolAcc{}
		)

		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			data, ok := strings.CutPrefix(line, "data:")
			if !ok {
				continue
			}
			data = strings.TrimSpace(data)

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
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}

			switch ev.Type {
			case "content_block_start":
				curBlock = ev.Index
				if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
					acc := &toolAcc{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
					tools = append(tools, acc)
					blockMap[curBlock] = acc
				}

			case "content_block_delta":
				if ev.Delta == nil {
					continue
				}
				switch ev.Delta.Type {
				case "text_delta", "":
					if ev.Delta.Text != "" {
						select {
						case out <- llm.Chunk{Delta: ev.Delta.Text}:
						case <-ctx.Done():
							return
						}
					}
				case "input_json_delta":
					if acc, ok := blockMap[ev.Index]; ok {
						acc.args.WriteString(ev.Delta.PartialJSON)
					}
				}

			case "message_delta":
				if ev.Usage != nil {
					usage.OutputTokens = ev.Usage.OutputTokens
				}
				if ev.Delta != nil && ev.Usage == nil {
					// message_delta 可能含 stop_reason 等,不需特殊处理
				}

			case "message_start":
				if ev.Usage != nil {
					usage.InputTokens = ev.Usage.InputTokens
				}

			case "message_stop":
				var tcs []llm.ToolCall
				for _, acc := range tools {
					args := acc.args.String()
					if args == "" {
						args = "{}"
					}
					tcs = append(tcs, llm.ToolCall{ID: acc.id, Name: acc.name, Arguments: json.RawMessage(args)})
				}
				out <- llm.Chunk{Done: true, ToolCalls: tcs, Usage: &usage}
				return
			}
		}
		if err := sc.Err(); err != nil {
			out <- llm.Chunk{Err: fmt.Errorf("anthropic: stream: %w", err)}
		}
	}()
	return out, nil
}
