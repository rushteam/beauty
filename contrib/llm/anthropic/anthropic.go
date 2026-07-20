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
	Model       string        `json:"model"`
	System      string        `json:"system,omitempty"`
	Messages    []llm.Message `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature,omitempty"`
	StopSeqs    []string      `json:"stop_sequences,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

func (c *Client) build(req llm.Request, stream bool) messagesReq {
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTok // Anthropic 要求 max_tokens 必填
	}
	return messagesReq{
		Model: req.Model, System: req.System, Messages: req.Messages,
		MaxTokens: maxTok, Temperature: req.Temperature, StopSeqs: req.Stop, Stream: stream,
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
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("anthropic: decode: %w", err)
	}
	var sb strings.Builder
	for _, blk := range out.Content {
		if blk.Type == "text" {
			sb.WriteString(blk.Text)
		}
	}
	return &llm.Response{
		Content:    sb.String(),
		Model:      out.Model,
		StopReason: out.StopReason,
		Usage:      llm.Usage{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens},
	}, nil
}

// Stream 实现 llm.Client(SSE:content_block_delta 增量、message_delta 带输出 token、message_stop)。
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
		var usage llm.Usage
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			data, ok := strings.CutPrefix(line, "data:")
			if !ok {
				continue
			}
			var ev struct {
				Type  string `json:"type"`
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(strings.TrimSpace(data)), &ev) != nil {
				continue
			}
			switch ev.Type {
			case "content_block_delta":
				if ev.Delta.Text != "" {
					select {
					case out <- llm.Chunk{Delta: ev.Delta.Text}:
					case <-ctx.Done():
						return
					}
				}
			case "message_delta":
				if ev.Usage != nil {
					usage.OutputTokens = ev.Usage.OutputTokens
				}
			case "message_stop":
				out <- llm.Chunk{Done: true, Usage: &usage}
				return
			}
		}
		if err := sc.Err(); err != nil {
			out <- llm.Chunk{Err: fmt.Errorf("anthropic: stream: %w", err)}
		}
	}()
	return out, nil
}
