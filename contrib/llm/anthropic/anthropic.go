// Package anthropic 是 llm.Client 的 Anthropic Messages API 实现,HTTP 直连 /v1/messages。
// BaseURL 可覆盖(网关/测试打桩)。纯标准库。
//
// 消息/工具/响应/流式事件的线格式翻译由 internal/anthropicwire 承担(与 llm/bedrock 上的
// Claude 复用同一份逻辑);本包只负责传输层:x-api-key 认证、anthropic-version 头、SSE 流式。
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/internal/anthropicwire"
)

const (
	defaultBaseURL = "https://api.anthropic.com/v1"
	apiVersion     = "2023-06-01"
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

// messagesReq 是 /v1/messages 的请求体。消息/工具字段用 anthropicwire 的线类型,
// 传输相关字段(model)在本包组装。
type messagesReq struct {
	Model       string                  `json:"model"`
	System      any                     `json:"system,omitempty"`
	Messages    []anthropicwire.Message `json:"messages"`
	MaxTokens   int                     `json:"max_tokens"`
	Temperature float64                 `json:"temperature,omitempty"`
	StopSeqs    []string                `json:"stop_sequences,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
	Tools       []anthropicwire.Tool    `json:"tools,omitempty"`
	ToolChoice  any                     `json:"tool_choice,omitempty"`
	Thinking    *thinkingReq            `json:"thinking,omitempty"`
}

type thinkingReq struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

func (c *Client) build(req llm.Request, stream bool) messagesReq {
	r := messagesReq{
		Model:      req.Model,
		System:     anthropicwire.BuildSystem(req.System, req.SystemCacheControl),
		Messages:   anthropicwire.BuildMessages(req.Messages),
		MaxTokens:  anthropicwire.ResolveMaxTokens(req.MaxTokens),
		StopSeqs:   req.Stop,
		Stream:     stream,
		Tools:      anthropicwire.BuildTools(req.Tools),
		ToolChoice: anthropicwire.BuildToolChoice(req.ToolChoice),
	}
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		r.Thinking = &thinkingReq{Type: "enabled", BudgetTokens: req.Thinking.BudgetTokens}
	} else {
		r.Temperature = req.Temperature
	}
	return r
}

func (c *Client) post(ctx context.Context, body any, betas ...string) (*http.Response, error) {
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
	if len(betas) > 0 {
		httpReq.Header.Set("anthropic-beta", strings.Join(betas, ","))
	}
	return c.hc.Do(httpReq)
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("anthropic: status %s: %s", resp.Status, bytes.TrimSpace(b))
}

// Generate 实现 llm.Client。
func (c *Client) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	betas := collectBetas(req)
	resp, err := c.post(ctx, c.build(req, false), betas...)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read: %w", err)
	}
	r, err := anthropicwire.ParseResponse(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: decode: %w", err)
	}
	return r, nil
}

// Stream 实现 llm.Client(SSE 事件流,支持文本增量、思考增量与流式 tool_calls 组装)。
// 事件语义由 anthropicwire.EventAccumulator 处理;本函数只负责逐行取 SSE data 并 yield。
func (c *Client) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		betas := collectBetas(req)
		resp, err := c.post(ctx, c.build(req, true), betas...)
		if err != nil {
			yield(llm.Chunk{}, fmt.Errorf("anthropic: request: %w", err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			yield(llm.Chunk{}, apiError(resp))
			return
		}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		acc := anthropicwire.NewEventAccumulator()
		for sc.Scan() {
			if err := ctx.Err(); err != nil {
				yield(llm.Chunk{}, err)
				return
			}
			line := strings.TrimSpace(sc.Text())
			data, ok := strings.CutPrefix(line, "data:")
			if !ok {
				continue
			}
			sd := acc.FeedExt([]byte(strings.TrimSpace(data)))
			if sd.Thinking != "" {
				if !yield(llm.Chunk{ThinkingDelta: sd.Thinking}, nil) {
					return
				}
			}
			if sd.Text != "" {
				if !yield(llm.Chunk{Delta: sd.Text}, nil) {
					return
				}
			}
			if sd.Done {
				tcs, usage := acc.Result()
				yield(llm.Chunk{ToolCalls: tcs, Usage: &usage, Thinking: acc.ThinkingText()}, nil)
				return
			}
		}
		if err := sc.Err(); err != nil {
			yield(llm.Chunk{}, fmt.Errorf("anthropic: stream: %w", err))
		}
	}
}

// collectBetas 根据请求内容收集需要的 anthropic-beta 头。
func collectBetas(req llm.Request) []string {
	var betas []string
	needCache := req.SystemCacheControl != ""
	if !needCache {
		for _, m := range req.Messages {
			if m.CacheControl != "" {
				needCache = true
				break
			}
		}
	}
	if needCache {
		betas = append(betas, "prompt-caching-2024-07-31")
	}
	return betas
}
