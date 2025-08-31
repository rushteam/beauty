package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// StaticTokenAuthenticator 静态令牌认证器（用于测试和简单场景）
type StaticTokenAuthenticator struct {
	tokens map[string]User // token -> user 映射
	mutex  sync.RWMutex
}

// NewStaticTokenAuthenticator 创建静态令牌认证器
func NewStaticTokenAuthenticator() *StaticTokenAuthenticator {
	return &StaticTokenAuthenticator{
		tokens: make(map[string]User),
	}
}

// AddToken 添加令牌和对应的用户
func (a *StaticTokenAuthenticator) AddToken(token string, user User) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.tokens[token] = user
}

// RemoveToken 移除令牌
func (a *StaticTokenAuthenticator) RemoveToken(token string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.tokens, token)
}

// Authenticate 认证令牌
func (a *StaticTokenAuthenticator) Authenticate(ctx context.Context, token string) (User, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	user, ok := a.tokens[token]
	if !ok {
		return nil, ErrInvalidToken
	}

	return user, nil
}

// JWTClaims JWT 声明
type JWTClaims struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Roles     []string  `json:"roles"`
	IssuedAt  time.Time `json:"iat"`
	ExpiresAt time.Time `json:"exp"`
}

// SimpleJWTAuthenticator 简单的 JWT 认证器（仅用于演示，生产环境建议使用专业的 JWT 库）
type SimpleJWTAuthenticator struct {
	secretKey []byte
}

// NewSimpleJWTAuthenticator 创建简单 JWT 认证器
func NewSimpleJWTAuthenticator(secretKey string) *SimpleJWTAuthenticator {
	return &SimpleJWTAuthenticator{
		secretKey: []byte(secretKey),
	}
}

// Authenticate 认证 JWT 令牌
func (a *SimpleJWTAuthenticator) Authenticate(ctx context.Context, token string) (User, error) {
	// 简单的 JWT 解析（生产环境请使用专业的 JWT 库）
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	// 验证签名
	expectedSig := a.sign(parts[0] + "." + parts[1])
	if parts[2] != expectedSig {
		return nil, ErrInvalidToken
	}

	// 解码载荷
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}

	var claims JWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrInvalidToken
	}

	// 检查过期时间
	if time.Now().After(claims.ExpiresAt) {
		return nil, ErrTokenExpired
	}

	user := NewUser(claims.UserID, claims.Username, claims.Roles)
	return user, nil
}

// sign 签名
func (a *SimpleJWTAuthenticator) sign(data string) string {
	h := hmac.New(sha256.New, a.secretKey)
	h.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// CreateToken 创建 JWT 令牌（用于测试）
func (a *SimpleJWTAuthenticator) CreateToken(userID, username string, roles []string, duration time.Duration) (string, error) {
	now := time.Now()
	claims := JWTClaims{
		UserID:    userID,
		Username:  username,
		Roles:     roles,
		IssuedAt:  now,
		ExpiresAt: now.Add(duration),
	}

	// 创建 header
	header := map[string]interface{}{
		"typ": "JWT",
		"alg": "HS256",
	}
	headerBytes, _ := json.Marshal(header)
	headerEncoded := base64.RawURLEncoding.EncodeToString(headerBytes)

	// 创建 payload
	payloadBytes, _ := json.Marshal(claims)
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payloadBytes)

	// 创建签名
	data := headerEncoded + "." + payloadEncoded
	signature := a.sign(data)

	return data + "." + signature, nil
}

// CallbackAuthenticator 回调认证器（允许业务方自定义认证逻辑）
type CallbackAuthenticator struct {
	AuthFunc func(ctx context.Context, token string) (User, error)
}

// NewCallbackAuthenticator 创建回调认证器
func NewCallbackAuthenticator(authFunc func(ctx context.Context, token string) (User, error)) *CallbackAuthenticator {
	return &CallbackAuthenticator{
		AuthFunc: authFunc,
	}
}

// Authenticate 执行认证回调
func (a *CallbackAuthenticator) Authenticate(ctx context.Context, token string) (User, error) {
	if a.AuthFunc == nil {
		return nil, fmt.Errorf("no authentication function provided")
	}
	return a.AuthFunc(ctx, token)
}

// ChainAuthenticator 链式认证器（按顺序尝试多个认证器）
type ChainAuthenticator struct {
	Authenticators []Authenticator
}

// NewChainAuthenticator 创建链式认证器
func NewChainAuthenticator(authenticators ...Authenticator) *ChainAuthenticator {
	return &ChainAuthenticator{
		Authenticators: authenticators,
	}
}

// Authenticate 按顺序尝试多个认证器
func (a *ChainAuthenticator) Authenticate(ctx context.Context, token string) (User, error) {
	var lastErr error

	for _, auth := range a.Authenticators {
		user, err := auth.Authenticate(ctx, token)
		if err == nil {
			return user, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return nil, ErrInvalidToken
}
