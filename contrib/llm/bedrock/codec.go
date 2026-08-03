package bedrock

import (
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// Codec 负责某一模型家族在 Bedrock 上的 body 构造与响应解析。传输层(SigV4、URL、
// event-stream 帧解码)与家族无关,由 Client 统一处理;各家族的差异全部收敛在 Codec 里。
//
// 选择:Client 按 model id 前缀从注册表挑 Codec(见 pickCodec),也可用 WithCodec 强制指定。
type Codec interface {
	// Name 是家族标识(用于日志/错误)。
	Name() string
	// BuildBody 把中立请求编成该家族的 invoke 请求体 JSON。stream 仅影响个别家族(多数家族
	// 请求体与是否流式无关,由 URL 决定)。
	BuildBody(req llm.Request, stream bool) ([]byte, error)
	// ParseResponse 解析非流式 invoke 的响应体。
	ParseResponse(body []byte) (*llm.Response, error)
	// NewStream 返回一个流式状态机,逐个吃"家族事件 JSON"(已从 event-stream chunk 帧里
	// base64 解码出的内层 payload),产出增量并在结束时给出 tool_calls/usage。
	NewStream() StreamState
}

// StreamState 是一次流式生成的累加状态(非并发安全,单 goroutine 使用)。
type StreamState interface {
	// Feed 吃一个家族事件 JSON。返回要下发的文本增量(可能空)与是否已到流末尾。
	Feed(event []byte) (delta string, done bool)
	// Result 返回累积的 tool_calls 与 usage(通常在 done 后调用)。
	Result() ([]llm.ToolCall, llm.Usage)
}

// EmbedCodec 是可选能力:家族支持文本向量时实现它(如 Amazon Titan Embeddings、Cohere Embed)。
type EmbedCodec interface {
	// BuildEmbedBody 为单条文本构造 embedding 请求体(Bedrock embedding 多为单文本/次)。
	BuildEmbedBody(text string) ([]byte, error)
	// ParseEmbedResponse 从 embedding 响应体取出向量。
	ParseEmbedResponse(body []byte) ([]float32, error)
}

// 各家族 codec 单例(无状态,流式状态在 NewStream 里新建)。
var (
	codecAnthropic Codec = anthropicCodec{}
	codecTitan     Codec = titanCodec{}
	codecLlama     Codec = llamaCodec{}
	codecMistral   Codec = mistralCodec{}
)

// regionPrefixes 是跨区推理 profile 的地域前缀(model id 形如 us.anthropic.claude-...)。
// 匹配家族前先剥掉它。
var regionPrefixes = []string{"us-gov.", "us.", "eu.", "apac.", "ap.", "ca.", "sa."}

// pickCodec 按 model id 选取家族 codec;无法识别时返回 nil。
func pickCodec(modelID string) Codec {
	id := stripRegionPrefix(strings.ToLower(modelID))
	switch {
	case strings.HasPrefix(id, "anthropic."):
		return codecAnthropic
	case strings.HasPrefix(id, "amazon.titan"): // titan-text / titan-embed 都由 titanCodec 处理
		return codecTitan
	case strings.HasPrefix(id, "meta.llama"):
		return codecLlama
	case strings.HasPrefix(id, "mistral."):
		return codecMistral
	default:
		return nil
	}
}

func stripRegionPrefix(id string) string {
	for _, p := range regionPrefixes {
		if strings.HasPrefix(id, p) {
			return id[len(p):]
		}
	}
	return id
}

// firstText 从中立消息里取纯文本表示(用于不支持多模态的家族:Parts 退化取其文本块)。
func firstText(m llm.Message) string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	var sb strings.Builder
	for _, p := range m.Parts {
		if p.Type == llm.PartText {
			sb.WriteString(p.Text)
		}
	}
	if sb.Len() == 0 {
		return m.Content
	}
	return sb.String()
}
