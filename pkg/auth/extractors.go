package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// HeaderTokenExtractor HTTP Header 令牌提取器
type HeaderTokenExtractor struct {
	HeaderName string // Header 名称，如 "Authorization"
	Prefix     string // 令牌前缀，如 "Bearer "
}

// NewHeaderTokenExtractor 创建 Header 令牌提取器
func NewHeaderTokenExtractor(headerName, prefix string) *HeaderTokenExtractor {
	return &HeaderTokenExtractor{
		HeaderName: headerName,
		Prefix:     prefix,
	}
}

// Extract 从 HTTP Header 中提取令牌
func (e *HeaderTokenExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	headers, ok := metadata["headers"].(map[string][]string)
	if !ok {
		return "", errors.New("no headers found in metadata")
	}

	values, ok := headers[e.HeaderName]
	if !ok || len(values) == 0 {
		return "", fmt.Errorf("header %s not found", e.HeaderName)
	}

	headerValue := values[0]
	if e.Prefix != "" {
		if !strings.HasPrefix(headerValue, e.Prefix) {
			return "", fmt.Errorf("header %s does not have expected prefix", e.HeaderName)
		}
		return strings.TrimPrefix(headerValue, e.Prefix), nil
	}

	return headerValue, nil
}

// QueryTokenExtractor URL 查询参数令牌提取器
type QueryTokenExtractor struct {
	ParamName string // 查询参数名称，如 "token"
}

// NewQueryTokenExtractor 创建查询参数令牌提取器
func NewQueryTokenExtractor(paramName string) *QueryTokenExtractor {
	return &QueryTokenExtractor{
		ParamName: paramName,
	}
}

// Extract 从查询参数中提取令牌
func (e *QueryTokenExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	query, ok := metadata["query"].(map[string][]string)
	if !ok {
		return "", errors.New("no query parameters found in metadata")
	}

	values, ok := query[e.ParamName]
	if !ok || len(values) == 0 {
		return "", fmt.Errorf("query parameter %s not found", e.ParamName)
	}

	return values[0], nil
}

// CookieTokenExtractor Cookie 令牌提取器
type CookieTokenExtractor struct {
	CookieName string // Cookie 名称
}

// NewCookieTokenExtractor 创建 Cookie 令牌提取器
func NewCookieTokenExtractor(cookieName string) *CookieTokenExtractor {
	return &CookieTokenExtractor{
		CookieName: cookieName,
	}
}

// Extract 从 Cookie 中提取令牌
func (e *CookieTokenExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	cookies, ok := metadata["cookies"].(map[string]string)
	if !ok {
		return "", errors.New("no cookies found in metadata")
	}

	token, ok := cookies[e.CookieName]
	if !ok {
		return "", fmt.Errorf("cookie %s not found", e.CookieName)
	}

	return token, nil
}

// MultiTokenExtractor 多源令牌提取器（按优先级尝试多个提取器）
type MultiTokenExtractor struct {
	Extractors []TokenExtractor
}

// NewMultiTokenExtractor 创建多源令牌提取器
func NewMultiTokenExtractor(extractors ...TokenExtractor) *MultiTokenExtractor {
	return &MultiTokenExtractor{
		Extractors: extractors,
	}
}

// Extract 按优先级尝试从多个源提取令牌
func (e *MultiTokenExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	var lastErr error

	for _, extractor := range e.Extractors {
		token, err := extractor.Extract(ctx, metadata)
		if err == nil {
			return token, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return "", fmt.Errorf("failed to extract token from any source: %w", lastErr)
	}

	return "", errors.New("no token extractors configured")
}

// gRPC Metadata 令牌提取器
type GRPCMetadataExtractor struct {
	MetadataKey string // gRPC metadata 键名
}

// NewGRPCMetadataExtractor 创建 gRPC metadata 令牌提取器
func NewGRPCMetadataExtractor(metadataKey string) *GRPCMetadataExtractor {
	return &GRPCMetadataExtractor{
		MetadataKey: metadataKey,
	}
}

// Extract 从 gRPC metadata 中提取令牌
func (e *GRPCMetadataExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	grpcMD, ok := metadata["grpc_metadata"].(map[string][]string)
	if !ok {
		return "", errors.New("no gRPC metadata found")
	}

	values, ok := grpcMD[e.MetadataKey]
	if !ok || len(values) == 0 {
		return "", fmt.Errorf("gRPC metadata key %s not found", e.MetadataKey)
	}

	return values[0], nil
}
