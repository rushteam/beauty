// Package token 提供会话令牌的全生命周期管理:签发、验证、续签、注销。
// 采用 dual token 模式(短命 session + 长命 refresh,分离密钥)+ 黑名单注销,
// 补齐 pkg/middleware/auth(只做验证)缺失的"签发/续签/注销"半边。
//
// 设计参考 Nakama server/core_session.go + session_cache.go:
//   - SessionToken claims: user_id + username + vars + token_id + exp + iat;
//   - refresh token 用独立密钥签发,携带 vars 以便续签时保留原值;
//   - 注销走黑名单:按 token_id 注销单会话,或按全局时间戳踢所有旧 token;
//   - 黑名单自动清理过期条目(ticker 周期剪枝)。
//
// JWT 签名复用 github.com/golang-jwt/jwt/v5(HS256)。
//
// 与 pkg/middleware/auth 的分工:auth 中间件从请求提取 token 调 token.Verify,
// 本包负责 token 的产生与失效。两者组合即完整登录态。
//
// 零值不可用,用 New 构造。Manager 并发安全。
package token

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 是 session token 的声明集合,嵌入 jwt.RegisteredClaims 提供标准字段。
type Claims struct {
	TokenID  string            `json:"tid"`             // 会话标识(非用户标识),用于黑名单注销
	UserID   string            `json:"sub"`             // 用户 ID(jwt sub)
	Username string            `json:"name,omitempty"`  // 用户名(可选)
	Vars     map[string]string `json:"vars,omitempty"`  // 业务元数据,免外查
	jwt.RegisteredClaims
}

// Manager 管理令牌签发、验证与注销。
type Manager struct {
	sessionKey []byte       // session token 签名密钥
	refreshKey []byte       // refresh token 签名密钥(独立)
	sessTTL    time.Duration
	refreshTTL time.Duration

	mu       sync.Mutex
	revoked  map[string]int64     // tokenID → 保留到(unix 秒);黑名单
	kickedAt map[string]int64     // userID → 全局失效时间秒;此时间前签发的 token 全失效

	stopCh chan struct{}
	stopped sync.Once
}

// Option 配置 Manager。
type Option func(*config)

type config struct {
	sessionKey []byte
	refreshKey []byte
	sessTTL    time.Duration
	refreshTTL time.Duration
}

// WithSessionKey 设置 session token 签名密钥(HS256,建议 32 字节)。
func WithSessionKey(key []byte) Option { return func(c *config) { c.sessionKey = key } }

// WithRefreshKey 设置 refresh token 签名密钥(独立于 session key)。
func WithRefreshKey(key []byte) Option { return func(c *config) { c.refreshKey = key } }

// WithSessionTTL session token 有效期(默认 1 小时)。
func WithSessionTTL(d time.Duration) Option { return func(c *config) { c.sessTTL = d } }

// WithRefreshTTL refresh token 有效期(默认 7 天)。
func WithRefreshTTL(d time.Duration) Option { return func(c *config) { c.refreshTTL = d } }

// ErrInvalidToken token 签名无效或格式错误。
var ErrInvalidToken = errors.New("token: invalid")

// ErrExpired token 已过期。
var ErrExpired = errors.New("token: expired")

// ErrRevoked token 已被注销(黑名单)。
var ErrRevoked = errors.New("token: revoked")

// ErrKicked 用户已被全局踢出(此时间前签发的 token 全部失效)。
var ErrKicked = errors.New("token: kicked")

// New 创建 Manager。sessionKey/refreshKey 为空则随机生成(适合单进程测试,
// 多进程需显式传入相同密钥)。
func New(opts ...Option) *Manager {
	cfg := &config{
		sessTTL:    time.Hour,
		refreshTTL: 7 * 24 * time.Hour,
	}
	for _, o := range opts {
		o(cfg)
	}
	if len(cfg.sessionKey) == 0 {
		cfg.sessionKey = randBytes(32)
	}
	if len(cfg.refreshKey) == 0 {
		cfg.refreshKey = randBytes(32)
	}
	m := &Manager{
		sessionKey: cfg.sessionKey,
		refreshKey: cfg.refreshKey,
		sessTTL:    cfg.sessTTL,
		refreshTTL: cfg.refreshTTL,
		revoked:    make(map[string]int64),
		kickedAt:   make(map[string]int64),
		stopCh:     make(chan struct{}),
	}
	go m.gc()
	return m
}

// Issue 签发一对 session + refresh token。tokenID 为空则随机生成。
// vars 嵌入 session token(业务元数据),refresh token 不带 vars。
func (m *Manager) Issue(userID, username string, vars map[string]string, tokenID string) (session, refresh string, err error) {
	now := time.Now()
	if tokenID == "" {
		tokenID = randID(16)
	}
	session, err = m.signSession(userID, username, tokenID, vars, now, m.sessTTL)
	if err != nil {
		return "", "", err
	}
	// refresh token 复用同 tokenID(便于注销时同步失效),携带 vars 以便续签保留。
	refresh, err = m.signRefresh(userID, username, tokenID, vars, now, m.refreshTTL)
	if err != nil {
		return "", "", err
	}
	return session, refresh, nil
}

// Verify 验证 session token,返回 claims。检查签名、过期、黑名单、全局踢出。
func (m *Manager) Verify(tokenStr string) (*Claims, error) {
	c, err := m.parse(m.sessionKey, tokenStr)
	if err != nil {
		return nil, err
	}
	if err := m.checkRevoked(c); err != nil {
		return nil, err
	}
	return c, nil
}

// VerifyRefresh 验证 refresh token。仅检查签名/过期/黑名单,不检查 vars。
func (m *Manager) VerifyRefresh(tokenStr string) (*Claims, error) {
	c, err := m.parse(m.refreshKey, tokenStr)
	if err != nil {
		return nil, err
	}
	if err := m.checkRevoked(c); err != nil {
		return nil, err
	}
	return c, nil
}

// Refresh 用 refresh token 换发新的 session token(复用原 tokenID,不产生新会话)。
// newVars 非 nil 则覆盖原 vars,nil 则保留 refresh token 中携带的原 vars。
func (m *Manager) Refresh(refreshToken string, newVars *map[string]string) (string, error) {
	c, err := m.VerifyRefresh(refreshToken)
	if err != nil {
		return "", err
	}
	vars := c.Vars
	if newVars != nil {
		vars = *newVars
	}
	return m.signSession(c.UserID, c.Username, c.TokenID, vars, time.Now(), m.sessTTL)
}

// Revoke 注销单个 token(按 tokenID)。该 token 的 session+refresh 同时失效。
func (m *Manager) Revoke(tokenID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[tokenID] = time.Now().Add(m.refreshTTL).Unix()
}

// RevokeAll 注销某用户的所有 token:记录全局失效时间,此前签发的全部失效。
// RevokeAll 之后新签发的 token(iat > kickedAt)不受影响。
// 秒级粒度(jwt iat 截断到秒);为正确踢出同秒内已签发的 token,采用 <=。
func (m *Manager) RevokeAll(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kickedAt[userID] = time.Now().Unix()
}

// checkRevoked 检查黑名单与全局踢出。
func (m *Manager) checkRevoked(c *Claims) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if exp, ok := m.revoked[c.TokenID]; ok {
		if time.Now().Unix() < exp {
			return ErrRevoked
		}
		delete(m.revoked, c.TokenID)
	}
	if c.IssuedAt != nil {
		if kicked, ok := m.kickedAt[c.UserID]; ok {
			if c.IssuedAt.Time.Unix() <= kicked {
				return ErrKicked
			}
		}
	}
	return nil
}

// gc 周期清理过期的黑名单条目。
func (m *Manager) gc() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			now := time.Now().Unix()
			for id, exp := range m.revoked {
				if now >= exp {
					delete(m.revoked, id)
				}
			}
			m.mu.Unlock()
		case <-m.stopCh:
			return
		}
	}
}

// Stop 停止 gc goroutine。幂等。
func (m *Manager) Stop() {
	m.stopped.Do(func() { close(m.stopCh) })
}

// ---- JWT HS256(via golang-jwt/jwt/v5) ----

func (m *Manager) signSession(userID, username, tokenID string, vars map[string]string, now time.Time, ttl time.Duration) (string, error) {
	c := Claims{
		TokenID:  tokenID,
		UserID:   userID,
		Username: username,
		Vars:     vars,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString(m.sessionKey)
}

func (m *Manager) signRefresh(userID, username, tokenID string, vars map[string]string, now time.Time, ttl time.Duration) (string, error) {
	c := Claims{
		TokenID:  tokenID,
		UserID:   userID,
		Username: username,
		Vars:     vars,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString(m.refreshKey)
}

// parse 解析并验证 token(签名 + 过期)。业务黑名单由 checkRevoked 处理。
func (m *Manager) parse(key []byte, tokenStr string) (*Claims, error) {
	c := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("%w: unexpected method %v", ErrInvalidToken, t.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpired
		}
		if errors.Is(err, jwt.ErrTokenSignatureInvalid) || errors.Is(err, jwt.ErrTokenMalformed) {
			return nil, ErrInvalidToken
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidToken, err)
	}
	return c, nil
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func randID(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
