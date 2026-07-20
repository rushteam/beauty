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

// Client 实现 llm.Client 与 llm.Embedder。
type Client struct {
	apiKey     string
	baseURL    string
	embedModel string
	hc         *http.Client
}

// Option 配置 Client。
type Option func(*Client)

// WithBaseURL 覆盖 API 基地址(默认 https://api.openai.com/v1)。
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient 使用自定义 *http.Client(超时、代理、otelhttp transport 等)。
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithEmbedModel 设置 Embed 使用的模型(默认 text-embedding-3-small)。
func WithEmbedModel(model string) Option { return func(c *Client) { c.embedModel = model } }

// New 创建 OpenAI 客户端。
func New(apiKey string, opts ...Option) *Client {
	c := &Client{apiKey: apiKey, baseURL: defaultBaseURL, hc: http.DefaultClient}
	for _, o := range opts {
		o(c)
	}
	return c
}

var (
	_ llm.Client   = (*Client)(nil)
	_ llm.Embedder = (*Client)(nil)
)

type chatReq struct {
	Model       string        `json:"model"`
	Messages    []llm.Message `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

func buildMessages(req llm.Request) []llm.Message {
	if req.System == "" {
		return req.Messages
	}
	return append([]llm.Message{{Role: llm.System, Content: req.System}}, req.Messages...)
}

func (c *Client) post(ctx context.Context, path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	return c.hc.Do(httpReq)
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("openai: status %s: %s", resp.Status, bytes.TrimSpace(b))
}

// Generate 实现 llm.Client。
func (c *Client) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	resp, err := c.post(ctx, "/chat/completions", chatReq{
		Model: req.Model, Messages: buildMessages(req),
		MaxTokens: req.MaxTokens, Temperature: req.Temperature, Stop: req.Stop,
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
			Message      llm.Message `json:"message"`
			FinishReason string      `json:"finish_reason"`
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
	}
	return r, nil
}

// Stream 实现 llm.Client(SSE:data: {json} ... data: [DONE])。
func (c *Client) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	body := chatReq{
		Model: req.Model, Messages: buildMessages(req),
		MaxTokens: req.MaxTokens, Temperature: req.Temperature, Stop: req.Stop, Stream: true,
	}
	resp, err := c.post(ctx, "/chat/completions", body)
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
	resp, err := c.post(ctx, "/embeddings", map[string]any{"model": model, "input": texts})
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
