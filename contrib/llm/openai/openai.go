// Package openai 是 llm.Client / llm.Embedder 的 OpenAI(及兼容网关)实现,HTTP 直连
// /v1/chat/completions 与 /v1/embeddings。BaseURL 可覆盖以对接 OpenAI 兼容的服务
// (本地模型、together、azure 网关、测试打桩)。纯标准库。
package openai

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

const defaultBaseURL = "https://api.openai.com/v1"

// OpenAI 兼容厂商的 BaseURL 预设——这些家都提供 OpenAI 兼容端点,直接
// New(key, WithBaseURL(...)) 即可,无需专门 provider。
const (
	BaseURLOpenAI    = "https://api.openai.com/v1"
	BaseURLDeepSeek  = "https://api.deepseek.com/v1"
	BaseURLMoonshot  = "https://api.moonshot.cn/v1"                        // Kimi / 月之暗面
	BaseURLZhipu     = "https://open.bigmodel.cn/api/paas/v4"              // 智谱 GLM
	BaseURLDashScope = "https://dashscope.aliyuncs.com/compatible-mode/v1" // 阿里通义千问
	BaseURLMiniMax   = "https://api.minimax.chat/v1"                       // MiniMax
)

// Client 实现 llm.Client 与 llm.Embedder。支持 OpenAI、OpenAI 兼容厂商(换 BaseURL)、
// 以及 Azure OpenAI(见 NewAzure)。
type Client struct {
	apiKey     string
	baseURL    string
	embedModel string
	hc         *http.Client

	// 认证与寻址(默认 OpenAI 语义;Azure 走 api-key 头 + deployment 路径 + api-version)
	keyHeader  string // 默认 "Authorization"
	keyPrefix  string // 默认 "Bearer "
	deployment string // 非空 → Azure 部署名,URL 走 /openai/deployments/<dep>/...
	apiVersion string // 非空 → 追加 ?api-version=<v>(Azure)
}

// Option 配置 Client。
type Option func(*Client)

// WithBaseURL 覆盖 API 基地址(默认 https://api.openai.com/v1;兼容厂商用上面的 BaseURL* 常量)。
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient 使用自定义 *http.Client(超时、代理、otelhttp transport 等)。
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithEmbedModel 设置 Embed 使用的模型(默认 text-embedding-3-small)。
func WithEmbedModel(model string) Option { return func(c *Client) { c.embedModel = model } }

// WithAPIKeyHeader 自定义认证头与前缀(默认 "Authorization" + "Bearer ")。
// Azure 用 ("api-key", "");某些网关可能用别的头。
func WithAPIKeyHeader(header, prefix string) Option {
	return func(c *Client) { c.keyHeader, c.keyPrefix = header, prefix }
}

// WithAzure 配置 Azure OpenAI 寻址:deployment 部署名 + api-version。
func WithAzure(deployment, apiVersion string) Option {
	return func(c *Client) { c.deployment, c.apiVersion = deployment, apiVersion }
}

// New 创建客户端。默认 OpenAI 认证(Authorization: Bearer <key>)。
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey: apiKey, baseURL: defaultBaseURL, hc: http.DefaultClient,
		keyHeader: "Authorization", keyPrefix: "Bearer ",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewAzure 创建 Azure OpenAI 客户端。endpoint 形如 https://<resource>.openai.azure.com,
// deployment 是部署名,apiVersion 如 "2024-10-21"。请求会走
// <endpoint>/openai/deployments/<deployment>/{chat/completions,embeddings}?api-version=<v>,
// 认证用 api-key 头。
func NewAzure(endpoint, deployment, apiVersion, apiKey string, opts ...Option) *Client {
	base := []Option{
		WithBaseURL(endpoint),
		WithAPIKeyHeader("api-key", ""),
		WithAzure(deployment, apiVersion),
	}
	return New(apiKey, append(base, opts...)...)
}

var (
	_ llm.Client   = (*Client)(nil)
	_ llm.Embedder = (*Client)(nil)
)

type chatReq struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
	Stop        []string     `json:"stop,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	ToolChoice  any          `json:"tool_choice,omitempty"`
}

// oaiMessage 是 OpenAI 的线上消息格式(与中立的 llm.Message 不同:工具调用用 tool_calls/
// tool_call_id 表达)。纯文本消息只有 role+content,序列化结果与旧版逐字节一致。
type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // OpenAI 用 JSON 字符串承载入参
	} `json:"function"`
}

type oaiTool struct {
	Type     string `json:"type"` // "function"
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

// buildMessages 把中立 Request 翻译成 OpenAI 线上消息(含 system 前置、工具调用/结果映射)。
func buildMessages(req llm.Request) []oaiMessage {
	msgs := make([]oaiMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, oaiMessage{Role: string(llm.System), Content: req.System})
	}
	for _, m := range req.Messages {
		om := oaiMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			oc := oaiToolCall{ID: tc.ID, Type: "function"}
			oc.Function.Name = tc.Name
			oc.Function.Arguments = string(tc.Arguments)
			om.ToolCalls = append(om.ToolCalls, oc)
		}
		msgs = append(msgs, om)
	}
	return msgs
}

func buildTools(defs []llm.ToolDef) []oaiTool {
	if len(defs) == 0 {
		return nil
	}
	ts := make([]oaiTool, len(defs))
	for i, d := range defs {
		ts[i].Type = "function"
		ts[i].Function.Name = d.Name
		ts[i].Function.Description = d.Description
		ts[i].Function.Parameters = d.Parameters
	}
	return ts
}

// buildToolChoice 把中立 ToolChoice 映射为 OpenAI 的 tool_choice(字符串或指定工具对象)。
func buildToolChoice(tc string) any {
	switch tc {
	case "":
		return nil
	case "auto", "none", "required":
		return tc
	default: // 具体工具名 → 强制调用它
		return map[string]any{"type": "function", "function": map[string]string{"name": tc}}
	}
}

// endpoint 按 OpenAI / Azure 语义构造某个 API 的完整 URL。kind 如 "chat/completions"、"embeddings"。
func (c *Client) endpoint(kind string) string {
	var u string
	if c.deployment != "" { // Azure
		u = c.baseURL + "/openai/deployments/" + c.deployment + "/" + kind
	} else {
		u = c.baseURL + "/" + kind
	}
	if c.apiVersion != "" {
		u += "?api-version=" + c.apiVersion
	}
	return u
}

func (c *Client) post(ctx context.Context, kind string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(kind), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(c.keyHeader, c.keyPrefix+c.apiKey)
	return c.hc.Do(httpReq)
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("openai: status %s: %s", resp.Status, bytes.TrimSpace(b))
}

// Generate 实现 llm.Client。
func (c *Client) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	resp, err := c.post(ctx, "chat/completions", chatReq{
		Model: req.Model, Messages: buildMessages(req),
		MaxTokens: req.MaxTokens, Temperature: req.Temperature, Stop: req.Stop,
		Tools: buildTools(req.Tools), ToolChoice: buildToolChoice(req.ToolChoice),
	})
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string        `json:"content"`
				ToolCalls []oaiToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	r := &llm.Response{Model: out.Model, Usage: llm.Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}}
	if len(out.Choices) > 0 {
		r.Content = out.Choices[0].Message.Content
		r.StopReason = out.Choices[0].FinishReason
		for _, tc := range out.Choices[0].Message.ToolCalls {
			r.ToolCalls = append(r.ToolCalls, llm.ToolCall{
				ID: tc.ID, Name: tc.Function.Name, Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}
	}
	return r, nil
}

// Stream 实现 llm.Client(SSE:data: {json} ... data: [DONE])。
func (c *Client) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	// 注:v1 的流式只透传文本增量,不解析流式 tool_calls 分片(工具循环走 Generate,见 llm/agent)。
	// tools 仍随请求发出,便于模型在最后一轮直接产出文本。
	body := chatReq{
		Model: req.Model, Messages: buildMessages(req),
		MaxTokens: req.MaxTokens, Temperature: req.Temperature, Stop: req.Stop, Stream: true,
		Tools: buildTools(req.Tools), ToolChoice: buildToolChoice(req.ToolChoice),
	}
	resp, err := c.post(ctx, "chat/completions", body)
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
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
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			data, ok := strings.CutPrefix(line, "data:")
			if !ok {
				continue
			}
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				out <- llm.Chunk{Done: true}
				return
			}
			var ev struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if len(ev.Choices) > 0 && ev.Choices[0].Delta.Content != "" {
				select {
				case out <- llm.Chunk{Delta: ev.Choices[0].Delta.Content}:
				case <-ctx.Done():
					return
				}
			}
		}
		if err := sc.Err(); err != nil {
			out <- llm.Chunk{Err: fmt.Errorf("openai: stream: %w", err)}
		}
	}()
	return out, nil
}

// Embed 实现 llm.Embedder(/v1/embeddings)。model 由 EmbedModel 指定,默认 text-embedding-3-small。
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	model := c.embedModel
	if model == "" {
		model = "text-embedding-3-small"
	}
	resp, err := c.post(ctx, "embeddings", map[string]any{"model": model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("openai: embed request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: embed decode: %w", err)
	}
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}
