package ratelimit

import (
	"context"
	"crypto/md5"
	"fmt"
	"net"
	"strings"
)

// IPKeyExtractor IP 地址键提取器
type IPKeyExtractor struct {
	// UseXForwardedFor 是否使用 X-Forwarded-For 头
	UseXForwardedFor bool
	// UseXRealIP 是否使用 X-Real-IP 头
	UseXRealIP bool
}

// NewIPKeyExtractor 创建 IP 地址键提取器
func NewIPKeyExtractor() *IPKeyExtractor {
	return &IPKeyExtractor{
		UseXForwardedFor: true,
		UseXRealIP:       true,
	}
}

// Extract 从请求中提取 IP 地址作为键
func (e *IPKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	// 尝试从 HTTP headers 获取真实 IP
	if headers, ok := metadata["headers"].(map[string][]string); ok {
		// 优先使用 X-Forwarded-For
		if e.UseXForwardedFor {
			if xff := headers["X-Forwarded-For"]; len(xff) > 0 {
				// X-Forwarded-For 可能包含多个 IP，取第一个
				ips := strings.Split(xff[0], ",")
				if len(ips) > 0 {
					ip := strings.TrimSpace(ips[0])
					if ip != "" {
						return fmt.Sprintf("ip:%s", ip), nil
					}
				}
			}
		}

		// 其次使用 X-Real-IP
		if e.UseXRealIP {
			if xri := headers["X-Real-IP"]; len(xri) > 0 {
				if ip := strings.TrimSpace(xri[0]); ip != "" {
					return fmt.Sprintf("ip:%s", ip), nil
				}
			}
		}
	}

	// 从 remote_addr 获取 IP
	if remoteAddr, ok := metadata["remote_addr"].(string); ok {
		if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
			return fmt.Sprintf("ip:%s", host), nil
		}
		// 如果没有端口，直接使用地址
		return fmt.Sprintf("ip:%s", remoteAddr), nil
	}

	// 从 gRPC peer 获取 IP
	if peerAddr, ok := metadata["peer_addr"].(string); ok {
		if host, _, err := net.SplitHostPort(peerAddr); err == nil {
			return fmt.Sprintf("ip:%s", host), nil
		}
		return fmt.Sprintf("ip:%s", peerAddr), nil
	}

	return "", fmt.Errorf("could not extract IP address")
}

// UserKeyExtractor 用户键提取器（基于用户ID）
type UserKeyExtractor struct {
	// UserIDKey 用户ID在元数据中的键名
	UserIDKey string
}

// NewUserKeyExtractor 创建用户键提取器
func NewUserKeyExtractor(userIDKey string) *UserKeyExtractor {
	if userIDKey == "" {
		userIDKey = "user_id"
	}
	return &UserKeyExtractor{
		UserIDKey: userIDKey,
	}
}

// Extract 从请求中提取用户ID作为键
func (e *UserKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	// 从上下文中获取用户信息
	if user := ctx.Value("user"); user != nil {
		if u, ok := user.(interface{ ID() string }); ok {
			return fmt.Sprintf("user:%s", u.ID()), nil
		}
	}

	// 从元数据中获取用户ID
	if userID, ok := metadata[e.UserIDKey].(string); ok && userID != "" {
		return fmt.Sprintf("user:%s", userID), nil
	}

	// 从 headers 中获取用户ID
	if headers, ok := metadata["headers"].(map[string][]string); ok {
		if userIDs := headers["X-User-ID"]; len(userIDs) > 0 {
			return fmt.Sprintf("user:%s", userIDs[0]), nil
		}
	}

	return "", fmt.Errorf("could not extract user ID")
}

// HeaderKeyExtractor HTTP Header 键提取器
type HeaderKeyExtractor struct {
	// HeaderName Header 名称
	HeaderName string
	// Prefix 键前缀
	Prefix string
}

// NewHeaderKeyExtractor 创建 Header 键提取器
func NewHeaderKeyExtractor(headerName, prefix string) *HeaderKeyExtractor {
	return &HeaderKeyExtractor{
		HeaderName: headerName,
		Prefix:     prefix,
	}
}

// Extract 从 HTTP Header 中提取键
func (e *HeaderKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	headers, ok := metadata["headers"].(map[string][]string)
	if !ok {
		return "", fmt.Errorf("no headers found in metadata")
	}

	values, ok := headers[e.HeaderName]
	if !ok || len(values) == 0 {
		return "", fmt.Errorf("header %s not found", e.HeaderName)
	}

	value := values[0]
	if e.Prefix != "" {
		return fmt.Sprintf("%s:%s", e.Prefix, value), nil
	}
	return value, nil
}

// PathKeyExtractor 路径键提取器
type PathKeyExtractor struct {
	// Prefix 键前缀
	Prefix string
	// StripQuery 是否去除查询参数
	StripQuery bool
}

// NewPathKeyExtractor 创建路径键提取器
func NewPathKeyExtractor(prefix string, stripQuery bool) *PathKeyExtractor {
	return &PathKeyExtractor{
		Prefix:     prefix,
		StripQuery: stripQuery,
	}
}

// Extract 从请求路径中提取键
func (e *PathKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	path, ok := metadata["path"].(string)
	if !ok {
		return "", fmt.Errorf("no path found in metadata")
	}

	if e.StripQuery {
		if idx := strings.Index(path, "?"); idx != -1 {
			path = path[:idx]
		}
	}

	if e.Prefix != "" {
		return fmt.Sprintf("%s:%s", e.Prefix, path), nil
	}
	return path, nil
}

// CompositeKeyExtractor 复合键提取器（组合多个提取器的结果）
type CompositeKeyExtractor struct {
	Extractors []KeyExtractor
	Separator  string
	Prefix     string
}

// NewCompositeKeyExtractor 创建复合键提取器
func NewCompositeKeyExtractor(separator, prefix string, extractors ...KeyExtractor) *CompositeKeyExtractor {
	if separator == "" {
		separator = ":"
	}
	return &CompositeKeyExtractor{
		Extractors: extractors,
		Separator:  separator,
		Prefix:     prefix,
	}
}

// Extract 组合多个提取器的结果
func (e *CompositeKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	var parts []string

	for _, extractor := range e.Extractors {
		key, err := extractor.Extract(ctx, metadata)
		if err == nil && key != "" {
			parts = append(parts, key)
		}
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("no keys extracted from any extractor")
	}

	result := strings.Join(parts, e.Separator)
	if e.Prefix != "" {
		result = e.Prefix + e.Separator + result
	}

	return result, nil
}

// HashKeyExtractor 哈希键提取器（对提取的键进行 MD5 哈希）
type HashKeyExtractor struct {
	Extractor KeyExtractor
	Prefix    string
}

// NewHashKeyExtractor 创建哈希键提取器
func NewHashKeyExtractor(extractor KeyExtractor, prefix string) *HashKeyExtractor {
	return &HashKeyExtractor{
		Extractor: extractor,
		Prefix:    prefix,
	}
}

// Extract 提取键并进行哈希
func (e *HashKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	key, err := e.Extractor.Extract(ctx, metadata)
	if err != nil {
		return "", err
	}

	// 计算 MD5 哈希
	hash := md5.Sum([]byte(key))
	hashStr := fmt.Sprintf("%x", hash)

	if e.Prefix != "" {
		return fmt.Sprintf("%s:%s", e.Prefix, hashStr), nil
	}
	return hashStr, nil
}

// CallbackKeyExtractor 回调键提取器（允许业务方自定义提取逻辑）
type CallbackKeyExtractor struct {
	ExtractFunc func(ctx context.Context, metadata map[string]interface{}) (string, error)
}

// NewCallbackKeyExtractor 创建回调键提取器
func NewCallbackKeyExtractor(extractFunc func(ctx context.Context, metadata map[string]interface{}) (string, error)) *CallbackKeyExtractor {
	return &CallbackKeyExtractor{
		ExtractFunc: extractFunc,
	}
}

// Extract 执行回调提取键
func (e *CallbackKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	if e.ExtractFunc == nil {
		return "", fmt.Errorf("no extract function provided")
	}
	return e.ExtractFunc(ctx, metadata)
}

// ChainKeyExtractor 链式键提取器（按顺序尝试多个提取器，返回第一个成功的结果）
type ChainKeyExtractor struct {
	Extractors []KeyExtractor
}

// NewChainKeyExtractor 创建链式键提取器
func NewChainKeyExtractor(extractors ...KeyExtractor) *ChainKeyExtractor {
	return &ChainKeyExtractor{
		Extractors: extractors,
	}
}

// Extract 按顺序尝试多个提取器
func (e *ChainKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
	var lastErr error

	for _, extractor := range e.Extractors {
		key, err := extractor.Extract(ctx, metadata)
		if err == nil && key != "" {
			return key, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return "", fmt.Errorf("failed to extract key from any extractor: %w", lastErr)
	}

	return "", fmt.Errorf("no key extractors configured")
}
