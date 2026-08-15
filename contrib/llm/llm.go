// Package llm 是 beauty 的 LLM 客户端薄机制:provider 无关的对话/流式/embedding/
// 文生图/图编辑/TTS 接口,外加失败切换、重试、用量计量中间件。作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/llm),**纯标准库、零外部依赖**——各家 provider 用
// HTTP 直连其 REST API,不引重型 SDK,也不 import beauty 核心。
//
// 分层:
//   - 本包:类型(Message/Request/Response/Chunk/Usage)、Client/Embedder/
//     ImageGenerator/ImageEditor/SpeechSynthesizer 接口、中间件(Fallback/Retry/Metered);
//   - 子包 llm/openai、llm/anthropic:各 provider 实现(HTTP + SSE 流式),BaseURL 可覆盖
//     (对接 OpenAI 兼容网关 / 本地模型 / 测试打桩)。
//
// 边界(机制而非策略):prompt 工程、选哪个模型、温度等参数、成本换算表都是 policy,由使用方定。
// 计量只吐 Usage/延迟,接哪(OTel/日志/账单)由 Metered 的回调决定,故本包不绑 OTel。
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
)

// Role 是对话角色。
type Role string

const (
	System    Role = "system"
	User      Role = "user"
	Assistant Role = "assistant"
	Tool      Role = "tool" // 工具执行结果消息(承载 ToolCallID 对应的返回)
)

// SourceType 标识消息的注入来源,用于 session 持久化时精确过滤:
// 避免 history provider 注入的消息被重复存储,或调试时追踪每条消息由哪个子系统产生。
type SourceType int

const (
	// SourceUser 用户或应用直接提供。
	SourceUser SourceType = iota
	// SourceHistory 由 HistoryProvider 从持久化存储加载的历史消息。
	SourceHistory
	// SourceContext 由 ContextProvider 注入的 RAG / Skills / 环境等上下文。
	SourceContext
	// SourceMiddleware 由 Agent 中间件注入(如 evaluator loop 的反馈消息)。
	SourceMiddleware
	// SourceModel 模型生成的 assistant 消息(工具调用循环中自动追加)。
	SourceModel
	// SourceTool 工具执行结果消息。
	SourceTool
)

// Message 是一条对话消息。纯文本对话只用 Role+Content;工具调用往返时,assistant 回合可能带
// ToolCalls(模型要求调用哪些工具),随后一条 Role=Tool、ToolCallID 指向该调用的消息回传结果。
// 各 provider 负责把本结构翻译成自家线格式(OpenAI tool_calls / Anthropic content blocks),
// 故本结构的字段是 provider 无关的中立表示,不直接当作某家的请求体。
//
// 多模态:Parts 非空时表示复合内容(文本+图片混排),Content 仍可用作纯文本后备。
// 纯文本消息保持 Content 即可,无需设 Parts。
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	Parts      []Part     `json:"parts,omitempty"`        // 多模态内容块(文本/图片)
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // 仅 assistant:模型请求调用的工具
	ToolCallID string     `json:"tool_call_id,omitempty"` // 仅 Role=Tool:对应 ToolCall.ID

	// CacheControl 标记此消息为缓存断点(Anthropic prompt caching)。
	// 设为 "ephemeral" 时,provider 在该消息的 content block 上附加 cache_control。
	// 不支持 prompt caching 的 provider 忽略此字段。
	CacheControl string `json:"cache_control,omitempty"`

	// Source 标识消息的注入来源。零值(SourceUser)表示用户或应用直接提供。
	// 用于 session 持久化过滤(避免 history 消息被重复存储)、调试追踪、审计。
	// Provider 序列化时忽略此字段——它是框架内部元数据,不发给模型。
	Source SourceType `json:"-"`
}

// PartType 标识内容块类型。
type PartType string

const (
	PartText  PartType = "text"
	PartImage PartType = "image"
)

// Part 是消息中的一个内容块(多模态)。
//   - text: Text 非空
//   - image: ImageURL 非空(http URL 或 data: base64),可选 Detail
type Part struct {
	Type     PartType `json:"type"`
	Text     string   `json:"text,omitempty"`
	ImageURL string   `json:"image_url,omitempty"`
	Detail   string   `json:"detail,omitempty"` // "auto"/"low"/"high"(OpenAI vision)
}

// ToolDef 声明一个可供模型调用的工具:名字、给模型看的描述、入参 JSON Schema。
// Parameters 是一个 JSON Schema object(可由使用方手写,或经 contrib/mcp 的反射产出),
// 各 provider 原样透传给模型。这里只是"声明",工具怎么执行是 policy(见 llm/agent)。
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall 是模型在一次生成里发起的一次工具调用请求。ID 是 provider 侧的调用标识,
// 回传结果时对应到 Message.ToolCallID;Arguments 是模型给出的入参(JSON,按 ToolDef.Parameters)。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ResponseFormat 控制模型输出格式。
type ResponseFormat struct {
	// Type: "text"(默认)/"json_object"/"json_schema"。
	Type string `json:"type"`
	// JSONSchema: 当 Type="json_schema" 时,给模型的 JSON Schema(OpenAI structured outputs)。
	// Name 是 schema 名(必填),Schema 是 JSON Schema 对象。
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

// JSONSchema 描述 structured output 的 schema。
type JSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict,omitempty"`
}

// ThinkingConfig 控制模型的深度推理/思考模式。
// Anthropic: extended thinking (type=enabled, budget_tokens);
// OpenAI: reasoning_effort (low/medium/high);
// 不支持的 provider 忽略此配置。
type ThinkingConfig struct {
	// Type: "enabled"(开启思考) / ""(不开启)。
	Type string `json:"type,omitempty"`
	// BudgetTokens 限制思考阶段最大 token 数(Anthropic extended thinking)。
	// 必须 >= 1024 且 < MaxTokens。
	BudgetTokens int `json:"budget_tokens,omitempty"`
	// ReasoningEffort 控制推理努力程度(OpenAI o-系列): "low" / "medium" / "high"。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// Request 是一次生成请求。System 便于单独给系统提示(Anthropic 用顶层 system,
// OpenAI provider 会转成一条 system 消息)。
type Request struct {
	Model       string
	Messages    []Message
	System      string
	MaxTokens   int
	Temperature float64
	Stop        []string

	// Tools 是本次可供模型调用的工具声明(为空则退化成纯对话)。
	Tools []ToolDef
	// ToolChoice 控制是否/如何调用工具:""或"auto"(模型自决)、"none"(禁用)、
	// "required"(必须调用某个)、或直接给某个工具名(强制调用它)。provider 各自映射。
	ToolChoice string

	// ResponseFormat 控制输出格式(json_object / json_schema / text)。
	// 零值或 Type="" 表示不限制(默认文本);provider 各自翻译,不支持则忽略。
	ResponseFormat *ResponseFormat

	// Thinking 控制模型的深度推理/思考模式。零值或 Type="" 表示不开启。
	Thinking *ThinkingConfig

	// SystemCacheControl 标记 system prompt 为缓存断点(Anthropic prompt caching)。
	// 设为 "ephemeral" 时,provider 在 system prompt 上附加 cache_control。
	SystemCacheControl string
}

// Usage 是 token 用量(用于计量/计费)。
type Usage struct {
	InputTokens  int
	OutputTokens int
	// CacheCreationInputTokens 是写入 prompt cache 的 token 数(Anthropic)。
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	// CacheReadInputTokens 是从 prompt cache 读取的 token 数(Anthropic)。
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
}

// Response 是一次非流式生成的结果。当模型决定调用工具时,ToolCalls 非空(此时 Content 可能为空),
// StopReason 通常为 "tool_calls"(OpenAI)/"tool_use"(Anthropic)。
type Response struct {
	Content    string
	Model      string
	StopReason string
	Usage      Usage
	ToolCalls  []ToolCall
	// Thinking 是模型的思考过程文本(extended thinking / reasoning)。
	// 仅当请求启用 Thinking 且模型支持时非空。
	Thinking string `json:"thinking,omitempty"`
}

// Chunk 是流式生成的一个增量片段。Delta 是本次新增文本;迭代结束时最后一个 Chunk 可能带最终 Usage。
//
// ToolCalls:支持流式工具调用的 provider 在流结束时带上组装好的完整调用列表
// (增量分片过程中可不填);agent 工具循环据此继续执行。
type Chunk struct {
	Delta         string
	ToolCalls     []ToolCall
	Usage         *Usage
	ThinkingDelta string `json:"thinking_delta,omitempty"`
	Thinking      string `json:"thinking,omitempty"`
}

// Client 是一个对话补全客户端(由各 provider 实现)。
type Client interface {
	// Generate 非流式生成。
	Generate(ctx context.Context, req Request) (*Response, error)
	// Stream 流式生成:每次 yield 产出一个 Chunk,error 非 nil 表示终止。
	// 调用方可随时 break 停止消费(天然背压)。
	Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error]
}

// Collect 消费整个 Stream 并组装成 Response(类似非流式 Generate 的结果)。
func Collect(seq iter.Seq2[Chunk, error]) (*Response, error) {
	var resp Response
	for c, err := range seq {
		if err != nil {
			return nil, err
		}
		resp.Content += c.Delta
		if c.ThinkingDelta != "" {
			resp.Thinking += c.ThinkingDelta
		}
		if len(c.ToolCalls) > 0 {
			resp.ToolCalls = c.ToolCalls
		}
		if c.Usage != nil {
			resp.Usage = *c.Usage
		}
		if c.Thinking != "" {
			resp.Thinking = c.Thinking
		}
	}
	return &resp, nil
}

// Embedder 生成文本向量(用于 RAG / 语义检索,配 contrib/vector)。
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// FilterBySource 返回仅包含指定来源类型消息的新切片。
func FilterBySource(msgs []Message, sources ...SourceType) []Message {
	set := make(map[SourceType]struct{}, len(sources))
	for _, s := range sources {
		set[s] = struct{}{}
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if _, ok := set[m.Source]; ok {
			out = append(out, m)
		}
	}
	return out
}

// ExcludeSource 返回排除指定来源类型消息后的新切片。
func ExcludeSource(msgs []Message, sources ...SourceType) []Message {
	set := make(map[SourceType]struct{}, len(sources))
	for _, s := range sources {
		set[s] = struct{}{}
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if _, ok := set[m.Source]; !ok {
			out = append(out, m)
		}
	}
	return out
}

// WithSource 返回一个消息副本,标记了指定的 SourceType。
func (m Message) WithSource(s SourceType) Message {
	m.Source = s
	return m
}

// MarkSource 为一批消息统一设置 SourceType,返回新切片。
func MarkSource(msgs []Message, s SourceType) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		m.Source = s
		out[i] = m
	}
	return out
}

// ErrNoClients 表示 Fallback 没有可用的下游 client。
var ErrNoClients = errors.New("llm: no clients")
