// Package llm 是 beauty 的 LLM 客户端薄机制:provider 无关的对话/流式/embedding 接口,
// 外加失败切换、重试、用量计量中间件。作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/llm),**纯标准库、零外部依赖**——各家 provider 用
// HTTP 直连其 REST API,不引重型 SDK,也不 import beauty 核心。
//
// 分层:
//   - 本包:类型(Message/Request/Response/Chunk/Usage)、Client/Embedder 接口、中间件
//     (Fallback/Retry/Metered);
//   - 子包 llm/openai、llm/anthropic:各 provider 实现(HTTP + SSE 流式),BaseURL 可覆盖
//     (对接 OpenAI 兼容网关 / 本地模型 / 测试打桩)。
//
// 边界(机制而非策略):prompt 工程、选哪个模型、温度等参数、成本换算表都是 policy,由使用方定。
// 计量只吐 Usage/延迟,接哪(OTel/日志/账单)由 Metered 的回调决定,故本包不绑 OTel。
package llm

import (
	"context"
	"errors"
)

// Role 是对话角色。
type Role string

const (
	System    Role = "system"
	User      Role = "user"
	Assistant Role = "assistant"
)

// Message 是一条对话消息。
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
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
}

// Usage 是 token 用量(用于计量/计费)。
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response 是一次非流式生成的结果。
type Response struct {
	Content    string
	Model      string
	StopReason string
	Usage      Usage
}

// Chunk 是流式生成的一个增量片段。Delta 是本次新增文本;结束时 Done=true 且可能带最终 Usage;
// 出错时 Err 非 nil(随后 channel 关闭)。
type Chunk struct {
	Delta string
	Usage *Usage
	Done  bool
	Err   error
}

// Client 是一个对话补全客户端(由各 provider 实现)。
type Client interface {
	// Generate 非流式生成。
	Generate(ctx context.Context, req Request) (*Response, error)
	// Stream 流式生成:返回的 channel 逐块产出 Delta,以 Done 或 Err 结束后关闭。
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

// Embedder 生成文本向量(用于 RAG / 语义检索,配 contrib/vector)。
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ErrNoClients 表示 Fallback 没有可用的下游 client。
var ErrNoClients = errors.New("llm: no clients")
