package llm

import (
	"context"
	"io"
)

// ImageGenerator 文生图(/v1/images/generations)。
type ImageGenerator interface {
	GenerateImage(ctx context.Context, req ImageRequest) (*ImageResponse, error)
}

// ImageEditor 图编辑(/v1/images/edits)。
type ImageEditor interface {
	EditImage(ctx context.Context, req ImageEditRequest) (*ImageResponse, error)
}

// SpeechSynthesizer 文本转语音(/v1/audio/speech)。
type SpeechSynthesizer interface {
	Speech(ctx context.Context, req SpeechRequest) ([]byte, error)
}

// ImageRequest 是文生图请求。
type ImageRequest struct {
	Model          string // 如 dall-e-3 / gpt-image-1
	Prompt         string
	N              int    // 生成张数;0 表示由服务端默认(通常 1)
	Size           string // 如 "1024x1024"
	Quality        string // "standard" / "hd" 等
	Style          string // "vivid" / "natural"(DALL·E 3)
	ResponseFormat string // "url"(默认) / "b64_json"
}

// ImageEditRequest 是图编辑请求。Image 必填(PNG);Mask 可选。
// Filename 用于 multipart 的文件名,空则默认 "image.png" / "mask.png"。
type ImageEditRequest struct {
	Model          string
	Prompt         string
	Image          io.Reader
	ImageFilename  string
	Mask           io.Reader // 可选
	MaskFilename   string
	N              int
	Size           string
	ResponseFormat string // "url"(默认) / "b64_json"
}

// ImageData 是一张生成/编辑结果图。
type ImageData struct {
	URL           string // ResponseFormat=url 时
	B64JSON       string // ResponseFormat=b64_json 时
	RevisedPrompt string // 部分模型会改写 prompt
}

// ImageResponse 是文生图/图编辑的结果。
type ImageResponse struct {
	Data []ImageData
}

// SpeechRequest 是 TTS 请求。
type SpeechRequest struct {
	Model          string  // 如 tts-1 / tts-1-hd
	Input          string  // 待合成文本
	Voice          string  // alloy / echo / fable / onyx / nova / shimmer 等
	ResponseFormat string  // mp3(默认) / opus / aac / flac / wav / pcm
	Speed          float64 // 0.25–4.0;0 表示服务端默认(1.0)
}
