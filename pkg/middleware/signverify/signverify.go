// Package signverify 提供基于 HMAC-SHA256 的 HTTP 请求签名校验中间件。
//
// 签名公式（默认）：hex(HMAC-SHA256(secret, timestamp + userID + body))
//
// 请求需携带以下 header（名称均可自定义）：
//   - X-App-Id:    调用方标识，用于查找对应的 secret
//   - X-Timestamp: Unix 秒级时间戳，超过 MaxAge 则拒绝
//   - X-Sign:      请求签名
//   - X-User-Id:   用户标识（参与签名计算，防止篡改）
//
// 可选功能：WithExtractUser() 在签名校验通过后自动将 X-User-Id 写入
// auth.User context，下游通过 auth.GetUserFromContext 读取。
//
// 两种预设模式：
//
//	// 直连（无网关）— 完整安全链：防重放 + 签名校验 + 身份提取
//	mux.Use(signverify.DirectMode(store, getSecret,
//	    signverify.WithDerivedKey(300),
//	    signverify.WithSkipPrefixes("/healthz"),
//	))
//
//	// 网关后 — 仅验网关签名 + 提取身份
//	mux.Use(signverify.BehindGateway(getSecret,
//	    signverify.WithSkipPrefixes("/healthz"),
//	))
//
// 也可直接使用底层 HTTPMiddleware 自由组合。
package signverify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/kvstore"
	"github.com/rushteam/beauty/pkg/middleware/antireplay"
	"github.com/rushteam/beauty/pkg/middleware/auth"
)

// SecretFunc 根据 appID 查找静态 secret。返回 false 表示未知 appID。
type SecretFunc func(appID string) (secret []byte, ok bool)

// SecretDeriver 根据 appID 和请求上下文动态派生 secret。
// 适用于基于请求内容（时间戳、region 等）派生密钥的场景，如 AWS SigV4 风格。
type SecretDeriver func(appID string, r *http.Request) (secret []byte, ok bool)

// DefaultWindowSec 派生 key 的默认时间窗口（秒）。
const DefaultWindowSec int64 = 300

type options struct {
	appIDHeader     string
	signHeader      string
	timestampHeader string
	userIDHeader    string
	maxAge          time.Duration
	skipPrefixes    []string
	extractUser     bool
	deriver         SecretDeriver
	deriveWindowSec int64 // >0 表示启用 DeriveKey 模式
	onReject        func(w http.ResponseWriter, reason string)
}

func defaults() *options {
	return &options{
		appIDHeader:     "X-App-Id",
		signHeader:      "X-Sign",
		timestampHeader: "X-Timestamp",
		userIDHeader:    "X-User-Id",
		maxAge:          5 * time.Minute,
		onReject:        defaultReject,
	}
}

// Option 配置 SignVerify 中间件。
type Option func(*options)

// WithAppIDHeader 自定义 appID header 名称，默认 "X-App-Id"。
func WithAppIDHeader(name string) Option {
	return func(o *options) { o.appIDHeader = name }
}

// WithSignHeader 自定义签名 header 名称，默认 "X-Sign"。
func WithSignHeader(name string) Option {
	return func(o *options) { o.signHeader = name }
}

// WithTimestampHeader 自定义时间戳 header 名称，默认 "X-Timestamp"。
func WithTimestampHeader(name string) Option {
	return func(o *options) { o.timestampHeader = name }
}

// WithUserIDHeader 自定义用户标识 header 名称，默认 "X-User-Id"。
func WithUserIDHeader(name string) Option {
	return func(o *options) { o.userIDHeader = name }
}

// WithMaxAge 自定义时间戳容差，默认 5 分钟。
func WithMaxAge(d time.Duration) Option {
	return func(o *options) { o.maxAge = d }
}

// WithSkipPrefixes 指定跳过校验的 URL 路径前缀。
func WithSkipPrefixes(prefixes ...string) Option {
	return func(o *options) { o.skipPrefixes = append(o.skipPrefixes, prefixes...) }
}

// WithExtractUser 启用后，签名校验通过时自动将用户标识 header 的值写入
// auth.User context（通过 auth.WithUser），下游用 auth.GetUserFromContext 读取。
func WithExtractUser() Option {
	return func(o *options) { o.extractUser = true }
}

// WithSecretDeriver 使用完全自定义的动态 key 派生替代静态 SecretFunc。
// 设置后 HTTPMiddleware 的 getSecret 参数将被忽略。
func WithSecretDeriver(fn SecretDeriver) Option {
	return func(o *options) { o.deriver = fn }
}

// WithDerivedKey 启用基于时间窗口的 key 派生模式。
//
// masterKey 始终不出现在网络上；客户端和服务端各自用 DeriveKey 从 masterKey
// 派生出短时效的 derivedKey 来签名。验签时自动尝试当前窗口和上一个窗口，
// 容忍窗口边界漂移。
//
// windowSec 为时间窗口秒数，传 0 使用默认值 300（5 分钟）。
//
// 用法：
//
//	signverify.HTTPMiddleware(getSecret, signverify.WithDerivedKey(300))
func WithDerivedKey(windowSec int64) Option {
	return func(o *options) {
		if windowSec <= 0 {
			windowSec = DefaultWindowSec
		}
		o.deriveWindowSec = windowSec
	}
}

// WithRejectHandler 自定义拒绝响应。reason 可能为：
// "missing_headers"、"invalid_timestamp"、"timestamp_expired"、"unknown_app"、"signature_mismatch"。
func WithRejectHandler(fn func(w http.ResponseWriter, reason string)) Option {
	return func(o *options) { o.onReject = fn }
}

// HTTPMiddleware 返回 HMAC 签名校验 HTTP 中间件。
// 若配置了 WithSecretDeriver，则 getSecret 可传 nil。
func HTTPMiddleware(getSecret SecretFunc, opts ...Option) func(http.Handler) http.Handler {
	o := defaults()
	for _, fn := range opts {
		fn(o)
	}
	maxAgeSec := int64(o.maxAge.Seconds())

	resolveSecret := func(appID string, r *http.Request) ([]byte, bool) {
		if o.deriver != nil {
			return o.deriver(appID, r)
		}
		return getSecret(appID)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkip(r.URL.Path, o.skipPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			appID := r.Header.Get(o.appIDHeader)
			sig := r.Header.Get(o.signHeader)
			tsStr := r.Header.Get(o.timestampHeader)
			if appID == "" || sig == "" || tsStr == "" {
				o.onReject(w, "missing_headers")
				return
			}

			ts, err := strconv.ParseInt(tsStr, 10, 64)
			if err != nil {
				o.onReject(w, "invalid_timestamp")
				return
			}
			if abs64(time.Now().Unix()-ts) > maxAgeSec {
				o.onReject(w, "timestamp_expired")
				return
			}

			secret, ok := resolveSecret(appID, r)
			if !ok {
				o.onReject(w, "unknown_app")
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read body failed", http.StatusInternalServerError)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			userID := r.Header.Get(o.userIDHeader)

			if !verifySignature(secret, tsStr, userID, body, sig, ts, o.deriveWindowSec) {
				o.onReject(w, "signature_mismatch")
				return
			}

			if o.extractUser && userID != "" {
				ctx := auth.WithUser(r.Context(), auth.NewUser(userID, "", nil))
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Sign 计算签名：hex(HMAC-SHA256(secret, timestamp + userID + body))。
// 公开此函数供客户端 SDK 生成签名。
func Sign(secret []byte, timestamp, userID string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte(userID))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// DeriveKey 从 masterKey + 时间窗口编号派生短时效签名 key。
// window = floor(unixTimestamp / windowSec)，masterKey 始终不出现在网络上。
//
// 客户端用法：
//
//	ts := time.Now().Unix()
//	dk := signverify.DeriveKey(masterSecret, ts, 300)
//	sig := signverify.Sign(dk, strconv.FormatInt(ts, 10), userID, body)
func DeriveKey(masterKey []byte, unixTimestamp int64, windowSec int64) []byte {
	if windowSec <= 0 {
		windowSec = DefaultWindowSec
	}
	window := unixTimestamp / windowSec
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte(strconv.FormatInt(window, 10)))
	return mac.Sum(nil)
}

// verifySignature 校验签名。deriveWindowSec > 0 时启用派生模式，
// 自动尝试当前窗口和上一个窗口的 derivedKey。
func verifySignature(secret []byte, tsStr, userID string, body []byte, sig string, ts, deriveWindowSec int64) bool {
	if deriveWindowSec <= 0 {
		expected := Sign(secret, tsStr, userID, body)
		return hmac.Equal([]byte(sig), []byte(expected))
	}
	for _, offset := range []int64{0, -1} {
		dk := DeriveKey(secret, ts+offset*deriveWindowSec, deriveWindowSec)
		expected := Sign(dk, tsStr, userID, body)
		if hmac.Equal([]byte(sig), []byte(expected)) {
			return true
		}
	}
	return false
}

func shouldSkip(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func defaultReject(w http.ResponseWriter, reason string) {
	switch reason {
	case "missing_headers":
		http.Error(w, "missing sign headers", http.StatusUnauthorized)
	case "invalid_timestamp":
		http.Error(w, "invalid timestamp", http.StatusUnauthorized)
	case "timestamp_expired":
		http.Error(w, "timestamp expired", http.StatusUnauthorized)
	case "unknown_app":
		http.Error(w, "unknown app_id", http.StatusUnauthorized)
	default:
		http.Error(w, "signature mismatch", http.StatusUnauthorized)
	}
}

// ---- 预设模式 ----

// DirectMode 返回直连场景（无 API 网关）的完整安全中间件，内部按顺序串联：
//
//  1. AntiReplay — nonce 防重放
//  2. SignVerify — HMAC 签名校验 + 自动提取用户身份
//
// 合成为单个 func(http.Handler) http.Handler，调用方无需关心顺序和组合。
// opts 同时作用于 AntiReplay 和 SignVerify（WithSkipPrefixes 等共享）。
//
// 典型用法（服务直接面向客户端）：
//
//	store := redis.NewStore(redisClient)
//	getSecret := func(appID string) ([]byte, bool) { return secrets[appID] }
//	mux.Use(signverify.DirectMode(store, getSecret,
//	    signverify.WithDerivedKey(300),
//	    signverify.WithSkipPrefixes("/healthz", "/callback/"),
//	))
func DirectMode(store kvstore.Store, getSecret SecretFunc, opts ...Option) func(http.Handler) http.Handler {
	o := defaults()
	for _, fn := range opts {
		fn(o)
	}

	var replayOpts []antireplay.Option
	if len(o.skipPrefixes) > 0 {
		replayOpts = append(replayOpts, antireplay.WithSkipPrefixes(o.skipPrefixes...))
	}
	antiReplayMW := antireplay.HTTPMiddleware(store, replayOpts...)

	signOpts := append(opts, WithExtractUser())
	signMW := HTTPMiddleware(getSecret, signOpts...)

	return func(next http.Handler) http.Handler {
		return antiReplayMW(signMW(next))
	}
}

// BehindGateway 返回网关后场景的中间件：仅校验网关签名 + 提取用户身份。
//
// 适用于：API 网关已完成客户端认证（JWT 等），用 HMAC 签名保护
// 转发给后端服务的请求，后端只需验证网关签名即可信任 X-User-Id。
//
// 典型用法（服务位于 API 网关之后）：
//
//	getSecret := func(appID string) ([]byte, bool) { return gatewaySecrets[appID] }
//	mux.Use(signverify.BehindGateway(getSecret,
//	    signverify.WithSkipPrefixes("/healthz"),
//	))
func BehindGateway(getSecret SecretFunc, opts ...Option) func(http.Handler) http.Handler {
	return HTTPMiddleware(getSecret, append(opts, WithExtractUser())...)
}
