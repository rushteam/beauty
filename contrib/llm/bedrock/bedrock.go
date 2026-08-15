// Package bedrock 是 llm.Client / llm.Embedder 的 AWS Bedrock 实现,HTTP 直连
// bedrock-runtime 的 /model/{id}/invoke 与 /invoke-with-response-stream。纯标准库:
// 自实现 SigV4 签名(sigv4.go)与 AWS event-stream 帧解码(eventstream.go),不引 AWS SDK。
//
// 一个 Client 可跨模型家族:按 llm.Request.Model 的 id 前缀选取 codec(Anthropic Claude / Amazon
// Titan / Meta Llama / Mistral),各家族的 body/响应差异收敛在 codec 里(codec*.go),传输层共用。
//
//	cli := bedrock.New("us-east-1") // 凭据默认取自 AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN
//	resp, _ := cli.Generate(ctx, llm.Request{
//	    Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
//	    Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
//	})
//
// Claude 家族支持 tools + 多模态 + 流式 tool_calls;Titan/Llama/Mistral 为纯文本(忽略 tools),
// 多轮消息按各家族官方 chat 模板拼成单 prompt。Embed 仅在选中家族支持向量时可用(如 Titan Embeddings)。
package bedrock

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

const (
	bedrockService = "bedrock"
	// defaultEmbedModel 是 Embed 未显式指定 embed 模型时的兜底(Titan Embeddings v2)。
	defaultEmbedModel = "amazon.titan-embed-text-v2:0"
)

// now 便于测试打桩时间;默认 time.Now。
var now = time.Now

// Client 实现 llm.Client(选中家族支持向量时亦实现 llm.Embedder)。
type Client struct {
	region     string
	creds      Credentials
	baseURL    string // 覆盖 endpoint(测试/VPC endpoint);默认按 region 拼
	codec      Codec  // 非 nil 时强制用它,忽略按 model id 的选择
	embedModel string
	hc         *http.Client
}

// Option 配置 Client。
type Option func(*Client)

// WithStaticCredentials 显式提供凭据(覆盖环境变量)。临时凭据把 sessionToken 一并传入。
func WithStaticCredentials(accessKeyID, secretAccessKey, sessionToken string) Option {
	return func(c *Client) {
		c.creds = Credentials{AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey, SessionToken: sessionToken}
	}
}

// WithHTTPClient 使用自定义 *http.Client。
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithBaseURL 覆盖 endpoint 基地址(默认 https://bedrock-runtime.{region}.amazonaws.com)。
// 用于测试打桩或 VPC/自定义 endpoint。
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithCodec 强制指定家族 codec,不再按 model id 自动选择(接入注册表外的自定义家族时用)。
func WithCodec(codec Codec) Option { return func(c *Client) { c.codec = codec } }

// WithEmbedModel 指定 Embed 使用的 model id(默认 amazon.titan-embed-text-v2:0)。
func WithEmbedModel(model string) Option { return func(c *Client) { c.embedModel = model } }

// New 创建 Bedrock 客户端。region 如 "us-east-1"。凭据默认取自环境变量
// AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN,可用 WithStaticCredentials 覆盖。
func New(region string, opts ...Option) *Client {
	c := &Client{
		region: region,
		creds: Credentials{
			AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
			SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		},
		embedModel: defaultEmbedModel,
		hc:         http.DefaultClient,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

var (
	_ llm.Client   = (*Client)(nil)
	_ llm.Embedder = (*Client)(nil)
)

func (c *Client) base() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return "https://bedrock-runtime." + c.region + ".amazonaws.com"
}

// codecFor 返回该 model id 对应的 codec(WithCodec 优先)。
func (c *Client) codecFor(modelID string) (Codec, error) {
	if c.codec != nil {
		return c.codec, nil
	}
	if cd := pickCodec(modelID); cd != nil {
		return cd, nil
	}
	return nil, fmt.Errorf("bedrock: no codec for model %q (用 WithCodec 指定家族)", modelID)
}

// invoke 对指定 endpoint 发一个已签名的 POST 并返回响应(调用方负责关 Body)。
func (c *Client) invoke(ctx context.Context, modelID, suffix string, body []byte, accept string) (*http.Response, error) {
	url := c.base() + "/model/" + modelPathEscape(modelID) + suffix
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	signV4(req, body, c.creds, bedrockService, c.region, now())
	return c.hc.Do(req)
}

// Generate 实现 llm.Client(非流式 /invoke)。
func (c *Client) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	codec, err := c.codecFor(req.Model)
	if err != nil {
		return nil, err
	}
	body, err := codec.BuildBody(req, false)
	if err != nil {
		return nil, fmt.Errorf("bedrock: build body: %w", err)
	}
	resp, err := c.invoke(ctx, req.Model, "/invoke", body, "application/json")
	if err != nil {
		return nil, fmt.Errorf("bedrock: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bedrock: read: %w", err)
	}
	r, err := codec.ParseResponse(rb)
	if err != nil {
		return nil, fmt.Errorf("bedrock: %s decode: %w", codec.Name(), err)
	}
	if r.Model == "" {
		r.Model = req.Model
	}
	return r, nil
}

// Stream 实现 llm.Client(/invoke-with-response-stream + event-stream 帧解码)。
func (c *Client) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		codec, err := c.codecFor(req.Model)
		if err != nil {
			yield(llm.Chunk{}, err)
			return
		}
		body, err := codec.BuildBody(req, true)
		if err != nil {
			yield(llm.Chunk{}, fmt.Errorf("bedrock: build body: %w", err))
			return
		}
		resp, err := c.invoke(ctx, req.Model, "/invoke-with-response-stream", body, "application/vnd.amazon.eventstream")
		if err != nil {
			yield(llm.Chunk{}, fmt.Errorf("bedrock: request: %w", err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			yield(llm.Chunk{}, apiError(resp))
			return
		}

		state := codec.NewStream()
		dec := NewDecoder(resp.Body)
		for {
			if err := ctx.Err(); err != nil {
				yield(llm.Chunk{}, err)
				return
			}
			frame, err := dec.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				yield(llm.Chunk{}, fmt.Errorf("bedrock: stream: %w", err))
				return
			}
			if frame.MessageType() == "exception" || frame.ExceptionType() != "" {
				yield(llm.Chunk{}, fmt.Errorf("bedrock: stream %s: %s", frame.ExceptionType(), bytes.TrimSpace(frame.Payload)))
				return
			}
			event, err := DecodeChunkBytes(frame.Payload)
			if err != nil {
				continue
			}
			delta, done := state.Feed(event)
			if delta != "" {
				if !yield(llm.Chunk{Delta: delta}, nil) {
					return
				}
			}
			if done {
				break
			}
		}
		tcs, usage := state.Result()
		yield(llm.Chunk{ToolCalls: tcs, Usage: &usage}, nil)
	}
}

// Embed 实现 llm.Embedder。仅当所选家族 codec 实现 EmbedCodec 时可用(如 Titan Embeddings)。
// Bedrock embedding 端点多为单文本/次,这里逐条调用。
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	codec, err := c.codecFor(c.embedModel)
	if err != nil {
		return nil, err
	}
	ec, ok := codec.(EmbedCodec)
	if !ok {
		return nil, fmt.Errorf("bedrock: model %q (%s) 不支持 embedding", c.embedModel, codec.Name())
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		body, err := ec.BuildEmbedBody(text)
		if err != nil {
			return nil, fmt.Errorf("bedrock: build embed body: %w", err)
		}
		resp, err := c.invoke(ctx, c.embedModel, "/invoke", body, "application/json")
		if err != nil {
			return nil, fmt.Errorf("bedrock: embed request: %w", err)
		}
		rb, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("bedrock: embed status %s: %s", resp.Status, bytes.TrimSpace(rb))
		}
		if readErr != nil {
			return nil, fmt.Errorf("bedrock: embed read: %w", readErr)
		}
		vec, err := ec.ParseEmbedResponse(rb)
		if err != nil {
			return nil, fmt.Errorf("bedrock: embed decode: %w", err)
		}
		out[i] = vec
	}
	return out, nil
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("bedrock: status %s: %s", resp.Status, bytes.TrimSpace(b))
}

// modelPathEscape 转义 model id 里的保留字符,使其能安全放进 URL path 段。
// model id 含 ':'(如 ...-v2:0),按 AWS SDK 约定把 ':' 编码为 '%3A'(uriEncode,不编码 '/';
// Bedrock model id 无 '/')。Go 会把该编码保留在 url.RawPath 中,使 EscapedPath()(既用于
// SigV4 canonical URI,又用于线上请求)返回同一编码串,签名与实际请求路径始终一致。
func modelPathEscape(id string) string {
	return uriEncode(id, false)
}
