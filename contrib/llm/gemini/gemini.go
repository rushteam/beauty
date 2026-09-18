// Package gemini 是 llm.Client / llm.Embedder 的 Google Gemini (Generative Language API) 原生实现。
// HTTP 直连 generativelanguage.googleapis.com 的 REST API,支持 generateContent(非流式)与
// streamGenerateContent(SSE 流式)。BaseURL 可覆盖以对接 Vertex AI 或自建代理。纯标准库。
//
// 与 openai/ provider 通过 OpenAI 兼容端点对接的方式不同,本包直接翻译 Gemini 原生线格式
// (functionDeclarations / functionCall / functionResponse / system_instruction / content.parts),
// 能完整支持 Gemini 的全部工具调用语义和特有功能。
//
// 认证双模式:
//   - API Key(默认):x-goog-api-key 头或 ?key= 查询参数,适用于 Google AI Studio。
//   - OAuth2 Bearer Token:WithTokenSource 注入 oauth2.TokenSource,适用于 Vertex AI。
package gemini

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
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// BaseURL 预设。
const (
	BaseURLGoogleAI = "https://generativelanguage.googleapis.com/v1beta"
)

// TokenSource 提供 OAuth2 access token(用于 Vertex AI 认证)。
// 与 golang.org/x/oauth2.TokenSource 签名兼容,但不强制 import——
// 使用方可传入 oauth2 的 TokenSource 或自行实现。
type TokenSource interface {
	Token() (*Token, error)
}

// Token 是 OAuth2 access token。
type Token struct {
	AccessToken string
}

// Client 实现 llm.Client 与 llm.Embedder。
type Client struct {
	apiKey      string
	baseURL     string
	hc          *http.Client
	embedModel  string
	tokenSource TokenSource
}

// Option 配置 Client。
type Option func(*Client)

// WithBaseURL 覆盖 API 基地址。
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient 使用自定义 *http.Client。
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.hc = hc }
}

// WithEmbedModel 设置 Embed 使用的默认模型。
func WithEmbedModel(model string) Option {
	return func(c *Client) { c.embedModel = model }
}

// WithTokenSource 使用 OAuth2 token 认证(Vertex AI)。设置后忽略 apiKey。
func WithTokenSource(ts TokenSource) Option {
	return func(c *Client) { c.tokenSource = ts }
}

// New 创建 Gemini 客户端。apiKey 用于 Google AI Studio 认证;Vertex AI 请用 NewVertexAI。
func New(apiKey string, opts ...Option) *Client {
	c := &Client{apiKey: apiKey, baseURL: defaultBaseURL, hc: http.DefaultClient}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewVertexAI 创建 Vertex AI Gemini 客户端。
// project: GCP 项目 ID;region: 如 "us-central1"。认证通过 TokenSource(通常是 ADC)。
//
// Vertex AI 端点格式:
//
//	https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/publishers/google/models/{model}:generateContent
//
// 使用示例:
//
//	import "golang.org/x/oauth2/google"
//	ts, _ := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
//	cli := gemini.NewVertexAI("my-project", "us-central1", &oauth2Adapter{ts}, opts...)
func NewVertexAI(project, region string, ts TokenSource, opts ...Option) *Client {
	base := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google",
		region, project, region)
	c := &Client{
		baseURL:     base,
		hc:          http.DefaultClient,
		tokenSource: ts,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// ---- 线格式类型(Gemini REST API) ----

type gContent struct {
	Role  string  `json:"role"`
	Parts []gPart `json:"parts"`
}

type gPart struct {
	Text             string             `json:"text,omitempty"`
	FunctionCall     *gFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *gFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *gInlineData       `json:"inlineData,omitempty"`
	Thought          *bool              `json:"thought,omitempty"`
}

type gFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type gFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type gInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gTool struct {
	FunctionDeclarations []gFuncDecl `json:"functionDeclarations,omitempty"`
}

type gFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type gToolConfig struct {
	FunctionCallingConfig *gFCConfig `json:"functionCallingConfig,omitempty"`
}

type gFCConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type gGenerateReq struct {
	Contents          []gContent   `json:"contents"`
	SystemInstruction *gContent    `json:"systemInstruction,omitempty"`
	Tools             []gTool      `json:"tools,omitempty"`
	ToolConfig        *gToolConfig `json:"toolConfig,omitempty"`
	GenerationConfig  *gGenConfig  `json:"generationConfig,omitempty"`
}

type gGenConfig struct {
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	ThinkingConfig   *gThink  `json:"thinkingConfig,omitempty"`
	ResponseMimeType string   `json:"responseMimeType,omitempty"`
	ResponseSchema   any      `json:"responseSchema,omitempty"`
}

type gThink struct {
	ThinkingBudget int `json:"thinkingBudget,omitempty"`
}

type gGenerateResp struct {
	Candidates    []gCandidate    `json:"candidates"`
	UsageMetadata *gUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string          `json:"modelVersion,omitempty"`
}

type gCandidate struct {
	Content      gContent `json:"content"`
	FinishReason string   `json:"finishReason"`
}

type gUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	ThinkingTokenCount   int `json:"thinkingTokenCount,omitempty"`
}

// ---- 中立层 → Gemini 线格式翻译 ----

func buildContents(req llm.Request) []gContent {
	// 构建 toolCallID → toolName 映射,供 functionResponse 使用
	nameByID := make(map[string]string)
	for _, m := range req.Messages {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				nameByID[tc.ID] = tc.Name
			}
		}
	}

	var out []gContent
	for i := 0; i < len(req.Messages); i++ {
		m := req.Messages[i]
		switch {
		case m.Role == llm.Tool:
			// Gemini 的 functionResponse 放在 role="user" 内
			parts := []gPart{buildFunctionResponse(m, nameByID)}
			// 连续的 tool 消息合并到同一个 user 回合
			for i+1 < len(req.Messages) && req.Messages[i+1].Role == llm.Tool {
				i++
				parts = append(parts, buildFunctionResponse(req.Messages[i], nameByID))
			}
			out = append(out, gContent{Role: "user", Parts: parts})

		case m.Role == llm.Assistant && len(m.ToolCalls) > 0:
			var parts []gPart
			if m.Content != "" {
				parts = append(parts, gPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				parts = append(parts, gPart{
					FunctionCall: &gFunctionCall{ID: tc.ID, Name: tc.Name, Args: args},
				})
			}
			out = append(out, gContent{Role: "model", Parts: parts})

		case m.Role == llm.Assistant:
			out = append(out, gContent{Role: "model", Parts: buildTextParts(m)})

		case m.Role == llm.System:
			// system 消息不走 contents,走 systemInstruction;这里忽略
			continue

		default: // user
			out = append(out, gContent{Role: "user", Parts: buildTextParts(m)})
		}
	}
	return out
}

func buildFunctionResponse(m llm.Message, nameByID map[string]string) gPart {
	// 尝试解析工具结果为 JSON object
	var respData map[string]any
	if err := json.Unmarshal([]byte(m.Content), &respData); err != nil {
		respData = map[string]any{"result": m.Content}
	}
	return gPart{
		FunctionResponse: &gFunctionResponse{
			ID:       m.ToolCallID,
			Name:     nameByID[m.ToolCallID],
			Response: respData,
		},
	}
}

func buildTextParts(m llm.Message) []gPart {
	if len(m.Parts) > 0 {
		var parts []gPart
		for _, p := range m.Parts {
			switch p.Type {
			case llm.PartText:
				parts = append(parts, gPart{Text: p.Text})
			case llm.PartImage:
				if strings.HasPrefix(p.ImageURL, "data:") {
					mt, data := parseDataURI(p.ImageURL)
					parts = append(parts, gPart{InlineData: &gInlineData{MimeType: mt, Data: data}})
				}
				// URL 图片需通过 File API 上传,此处不支持——降级忽略
			}
		}
		return parts
	}
	return []gPart{{Text: m.Content}}
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

func buildSystemInstruction(system string) *gContent {
	if system == "" {
		return nil
	}
	return &gContent{Parts: []gPart{{Text: system}}}
}

func buildTools(defs []llm.ToolDef) []gTool {
	if len(defs) == 0 {
		return nil
	}
	decls := make([]gFuncDecl, len(defs))
	for i, d := range defs {
		params := d.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		decls[i] = gFuncDecl{Name: d.Name, Description: d.Description, Parameters: params}
	}
	return []gTool{{FunctionDeclarations: decls}}
}

func buildToolConfig(tc string) *gToolConfig {
	switch tc {
	case "", "auto":
		return &gToolConfig{FunctionCallingConfig: &gFCConfig{Mode: "AUTO"}}
	case "none":
		return &gToolConfig{FunctionCallingConfig: &gFCConfig{Mode: "NONE"}}
	case "required":
		return &gToolConfig{FunctionCallingConfig: &gFCConfig{Mode: "ANY"}}
	default:
		// 指定工具名
		return &gToolConfig{FunctionCallingConfig: &gFCConfig{
			Mode:                 "ANY",
			AllowedFunctionNames: []string{tc},
		}}
	}
}

func buildGenerateReq(req llm.Request) gGenerateReq {
	gr := gGenerateReq{
		Contents:          buildContents(req),
		SystemInstruction: buildSystemInstruction(req.System),
		Tools:             buildTools(req.Tools),
	}
	if len(req.Tools) > 0 {
		gr.ToolConfig = buildToolConfig(req.ToolChoice)
	}

	gc := &gGenConfig{}
	hasGC := false
	if req.MaxTokens > 0 {
		gc.MaxOutputTokens = req.MaxTokens
		hasGC = true
	}
	if req.Temperature > 0 {
		t := req.Temperature
		gc.Temperature = &t
		hasGC = true
	}
	if len(req.Stop) > 0 {
		gc.StopSequences = req.Stop
		hasGC = true
	}
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		gc.ThinkingConfig = &gThink{ThinkingBudget: req.Thinking.BudgetTokens}
		hasGC = true
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		gc.ResponseMimeType = "application/json"
		hasGC = true
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" && req.ResponseFormat.JSONSchema != nil {
		gc.ResponseMimeType = "application/json"
		var schema any
		if json.Unmarshal(req.ResponseFormat.JSONSchema.Schema, &schema) == nil {
			gc.ResponseSchema = schema
		}
		hasGC = true
	}
	if hasGC {
		gr.GenerationConfig = gc
	}
	return gr
}

// ---- Gemini 响应 → 中立层翻译 ----

func parseResponse(gr *gGenerateResp) *llm.Response {
	r := &llm.Response{Model: gr.ModelVersion}
	if gr.UsageMetadata != nil {
		r.Usage = llm.Usage{
			InputTokens:  gr.UsageMetadata.PromptTokenCount,
			OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
		}
	}
	if len(gr.Candidates) == 0 {
		return r
	}
	cand := gr.Candidates[0]
	r.StopReason = cand.FinishReason

	var textParts []string
	var thinkParts []string
	for _, p := range cand.Content.Parts {
		if p.FunctionCall != nil {
			r.ToolCalls = append(r.ToolCalls, llm.ToolCall{
				ID:        p.FunctionCall.ID,
				Name:      p.FunctionCall.Name,
				Arguments: p.FunctionCall.Args,
			})
			continue
		}
		if p.Thought != nil && *p.Thought {
			thinkParts = append(thinkParts, p.Text)
			continue
		}
		if p.Text != "" {
			textParts = append(textParts, p.Text)
		}
	}
	r.Content = strings.Join(textParts, "")
	r.Thinking = strings.Join(thinkParts, "")
	return r
}

// ---- HTTP 传输 ----

func (c *Client) endpoint(model, method string) string {
	return fmt.Sprintf("%s/models/%s:%s", c.baseURL, model, method)
}

func (c *Client) setAuth(req *http.Request) error {
	if c.tokenSource != nil {
		tok, err := c.tokenSource.Token()
		if err != nil {
			return fmt.Errorf("gemini: token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		return nil
	}
	if c.apiKey != "" {
		req.Header.Set("x-goog-api-key", c.apiKey)
	}
	return nil
}

func (c *Client) post(ctx context.Context, url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.setAuth(req); err != nil {
		return nil, err
	}
	return c.hc.Do(req)
}

func apiError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, string(body))
}

// ---- llm.Client 实现 ----

// Generate 非流式生成。
func (c *Client) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	gr := buildGenerateReq(req)
	resp, err := c.post(ctx, c.endpoint(req.Model, "generateContent"), gr)
	if err != nil {
		return nil, fmt.Errorf("gemini: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	var out gGenerateResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gemini: decode: %w", err)
	}
	r := parseResponse(&out)
	if r.Model == "" {
		r.Model = req.Model
	}
	return r, nil
}

// Stream 流式生成(SSE:data: {json})。Gemini 每个 SSE 事件是一个完整的 GenerateContentResponse。
func (c *Client) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		gr := buildGenerateReq(req)
		url := c.endpoint(req.Model, "streamGenerateContent") + "?alt=sse"
		resp, err := c.post(ctx, url, gr)
		if err != nil {
			yield(llm.Chunk{}, fmt.Errorf("gemini: request: %w", err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			yield(llm.Chunk{}, apiError(resp))
			return
		}

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var prevText string
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
			data = strings.TrimSpace(data)
			if data == "" {
				continue
			}

			var ev gGenerateResp
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if len(ev.Candidates) == 0 {
				continue
			}
			cand := ev.Candidates[0]

			var chunk llm.Chunk
			for _, p := range cand.Content.Parts {
				if p.FunctionCall != nil {
					chunk.ToolCalls = append(chunk.ToolCalls, llm.ToolCall{
						ID:        p.FunctionCall.ID,
						Name:      p.FunctionCall.Name,
						Arguments: p.FunctionCall.Args,
					})
					continue
				}
				if p.Thought != nil && *p.Thought {
					chunk.ThinkingDelta = p.Text
					continue
				}
				if p.Text != "" {
					// Gemini 流式返回累积文本,需要计算增量
					if strings.HasPrefix(p.Text, prevText) {
						chunk.Delta = p.Text[len(prevText):]
					} else {
						chunk.Delta = p.Text
					}
					prevText = p.Text
				}
			}
			if ev.UsageMetadata != nil {
				chunk.Usage = &llm.Usage{
					InputTokens:  ev.UsageMetadata.PromptTokenCount,
					OutputTokens: ev.UsageMetadata.CandidatesTokenCount,
				}
			}

			if chunk.Delta != "" || len(chunk.ToolCalls) > 0 || chunk.ThinkingDelta != "" || chunk.Usage != nil {
				if !yield(chunk, nil) {
					return
				}
			}
		}
		if err := sc.Err(); err != nil {
			yield(llm.Chunk{}, fmt.Errorf("gemini: stream: %w", err))
		}
	}
}

// ---- llm.Embedder 实现 ----

type gEmbedReq struct {
	Model   string   `json:"model"`
	Content gContent `json:"content"`
}

type gBatchEmbedReq struct {
	Requests []gEmbedReq `json:"requests"`
}

type gEmbedResp struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

// Embed 生成文本向量。使用 batchEmbedContents 端点,一次请求批量处理。
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	model := c.embedModel
	if model == "" {
		model = "text-embedding-004"
	}
	reqs := make([]gEmbedReq, len(texts))
	for i, t := range texts {
		reqs[i] = gEmbedReq{
			Model:   fmt.Sprintf("models/%s", model),
			Content: gContent{Parts: []gPart{{Text: t}}},
		}
	}
	url := fmt.Sprintf("%s/models/%s:batchEmbedContents", c.baseURL, model)
	resp, err := c.post(ctx, url, gBatchEmbedReq{Requests: reqs})
	if err != nil {
		return nil, fmt.Errorf("gemini: embed request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	var out gEmbedResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gemini: embed decode: %w", err)
	}
	result := make([][]float32, len(out.Embeddings))
	for i, e := range out.Embeddings {
		result[i] = e.Values
	}
	return result, nil
}
