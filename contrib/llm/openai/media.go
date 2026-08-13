package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/rushteam/beauty/contrib/llm"
)

var (
	_ llm.ImageGenerator    = (*Client)(nil)
	_ llm.ImageEditor       = (*Client)(nil)
	_ llm.SpeechSynthesizer = (*Client)(nil)
)

// GenerateImage 实现 llm.ImageGenerator(/v1/images/generations)。
func (c *Client) GenerateImage(ctx context.Context, req llm.ImageRequest) (*llm.ImageResponse, error) {
	body := map[string]any{"prompt": req.Prompt}
	if req.Model != "" {
		body["model"] = req.Model
	}
	if req.N > 0 {
		body["n"] = req.N
	}
	if req.Size != "" {
		body["size"] = req.Size
	}
	if req.Quality != "" {
		body["quality"] = req.Quality
	}
	if req.Style != "" {
		body["style"] = req.Style
	}
	if req.ResponseFormat != "" {
		body["response_format"] = req.ResponseFormat
	}
	resp, err := c.post(ctx, "images/generations", body)
	if err != nil {
		return nil, fmt.Errorf("openai: image generate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	return decodeImageResponse(resp.Body)
}

// EditImage 实现 llm.ImageEditor(/v1/images/edits)。走 multipart/form-data。
func (c *Client) EditImage(ctx context.Context, req llm.ImageEditRequest) (*llm.ImageResponse, error) {
	if req.Image == nil {
		return nil, fmt.Errorf("openai: image edit: Image is required")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	imgName := req.ImageFilename
	if imgName == "" {
		imgName = "image.png"
	}
	part, err := w.CreateFormFile("image", imgName)
	if err != nil {
		return nil, fmt.Errorf("openai: image edit form: %w", err)
	}
	if _, err := io.Copy(part, req.Image); err != nil {
		return nil, fmt.Errorf("openai: image edit copy image: %w", err)
	}

	if req.Mask != nil {
		maskName := req.MaskFilename
		if maskName == "" {
			maskName = "mask.png"
		}
		mp, err := w.CreateFormFile("mask", maskName)
		if err != nil {
			return nil, fmt.Errorf("openai: image edit form mask: %w", err)
		}
		if _, err := io.Copy(mp, req.Mask); err != nil {
			return nil, fmt.Errorf("openai: image edit copy mask: %w", err)
		}
	}

	fields := map[string]string{"prompt": req.Prompt}
	if req.Model != "" {
		fields["model"] = req.Model
	}
	if req.N > 0 {
		fields["n"] = strconv.Itoa(req.N)
	}
	if req.Size != "" {
		fields["size"] = req.Size
	}
	if req.ResponseFormat != "" {
		fields["response_format"] = req.ResponseFormat
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("openai: image edit field %s: %w", k, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("openai: image edit close form: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("images/edits"), &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", w.FormDataContentType())
	httpReq.Header.Set(c.keyHeader, c.keyPrefix+c.apiKey)
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: image edit: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	return decodeImageResponse(resp.Body)
}

// Speech 实现 llm.SpeechSynthesizer(/v1/audio/speech)。返回原始音频字节。
func (c *Client) Speech(ctx context.Context, req llm.SpeechRequest) ([]byte, error) {
	body := map[string]any{
		"model": req.Model,
		"input": req.Input,
		"voice": req.Voice,
	}
	if req.ResponseFormat != "" {
		body["response_format"] = req.ResponseFormat
	}
	if req.Speed > 0 {
		body["speed"] = req.Speed
	}
	resp, err := c.post(ctx, "audio/speech", body)
	if err != nil {
		return nil, fmt.Errorf("openai: speech: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: speech read: %w", err)
	}
	return b, nil
}

func decodeImageResponse(r io.Reader) (*llm.ImageResponse, error) {
	var out struct {
		Data []struct {
			URL           string `json:"url"`
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: image decode: %w", err)
	}
	res := &llm.ImageResponse{Data: make([]llm.ImageData, len(out.Data))}
	for i, d := range out.Data {
		res.Data[i] = llm.ImageData{
			URL:           d.URL,
			B64JSON:       d.B64JSON,
			RevisedPrompt: d.RevisedPrompt,
		}
	}
	return res, nil
}
