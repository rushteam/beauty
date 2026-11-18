// Package botguard 提供 Web 端防重放 + 反自动化的风控签名机制（a_bogus / X-Bogus 一类方案的服务端与浏览器 SDK）。
//
// 整体由两部分协同：
//
//	[浏览器 SDK botguard.js]                         [服务端 Guard.Middleware]
//	1. 采集指纹 (Canvas/WebGL/Audio/UA/硬件)          1. 令牌解封（时效、IP 段、指纹绑定）
//	2. 采集行为 (鼠标轨迹/点击/键盘/滚动/停留)  ──▶   2. 签名解密 + 业务摘要比对（防篡改）
//	3. 请求摘要 = SHA256(method, path, query, body)  3. 时间窗口 + Nonce 唯一（防重放）
//	4. Cipher.Seal(会话密钥, 载荷, AAD=请求摘要)     4. 规则评分 → 放行 / PoW 挑战 / 蜜罐 / 拦截
//
// 签名算法可插拔，默认 AESGCM（前缀 v1）。WithCipher 换成魔改实现，WithAcceptCiphers
// 在轮换期继续校验旧算法。浏览器侧用 BotGuard.registerCipher 注册同一 ID 的实现。
// 令牌仍由服务端 AES-GCM 密封，会话密钥由 HMAC(master, tokenID) 派生，
// 服务端无需存储会话状态；Nonce、挑战通过记录、频控计数依赖 kvstore.Store（生产用 Redis 实现）。
//
// 安全边界：前端代码对攻击者永远透明，SDK 上报的环境/行为字段可以被伪造。真正有效的是
// 服务端可独立验证的部分（签名绑定、时效、Nonce、令牌与 IP/指纹绑定、频控、UA），
// 端侧特征只用于抬高成本。发布前应对 botguard.js 做混淆或 JSVMP 虚拟化，并定期轮换算法版本。
//
// 典型用法：
//
//	g, _ := botguard.New(redisStore, masterKey)
//	mux.Handle("POST /_botguard/token", g.TokenHandler())
//	mux.Handle("GET /_botguard/sdk.js", botguard.SDKHandler())
//	api := g.Middleware()(apiMux)
//
//	// 配置中心一键降级：只观测不拦截
//	g.SetMode(botguard.ModeMonitor)
package botguard

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/store/kvstore"
)

// Mode 运行模式，可在运行时通过 SetMode 切换（对接配置中心实现一键降级）。
type Mode int32

const (
	// ModeEnforce 完整校验并按判定处置。
	ModeEnforce Mode = iota
	// ModeMonitor 完整校验、回调 OnVerdict，但一律放行。用于灰度新规则或紧急降级。
	ModeMonitor
	// ModeOff 完全跳过校验。
	ModeOff
)

// Action 判定后的处置动作。
type Action int

const (
	ActionAllow     Action = iota // 放行
	ActionChallenge               // 二次挑战（PoW / 验证码）
	ActionDeceive                 // 返回蜜罐数据
	ActionBlock                   // 拦截
)

func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "allow"
	case ActionChallenge:
		return "challenge"
	case ActionDeceive:
		return "deceive"
	default:
		return "block"
	}
}

// 硬校验失败原因（Verdict.Failure）。
const (
	FailTokenMissing = "token_missing"
	FailTokenInvalid = "token_invalid"
	FailTokenExpired = "token_expired"
	FailSignMissing  = "sign_missing"
	FailSignInvalid  = "sign_invalid"
	FailTimestamp    = "timestamp_expired"
	FailReplay       = "replay"
	FailBodyTooLarge = "body_too_large"
	FailBodyRead     = "body_read"
	FailStore        = "store_error"
)

// Verdict 是一次请求的风控判定结果，可通过 FromContext 在下游读取。
type Verdict struct {
	Action  Action
	Failure string   // 非空表示硬校验失败，此时 Action 为 ActionBlock
	Score   int      // 规则累计风险分
	Reasons []string // 命中的规则名
	Cleared bool     // 原本需挑战，因已通过 PoW / 验证码而放行
	TokenID string
	// TokenAge 令牌已签发时长。
	TokenAge time.Duration
	Signals  *Signals // 硬校验通过后才有
}

// Payload 是签名内携带的明文载荷（客户端加密，服务端解密）。
type Payload struct {
	TS          int64    `json:"ts"` // 客户端时间（毫秒，已按令牌下发时的服务端时间校正）
	Nonce       string   `json:"n"`
	Fingerprint string   `json:"fp"`
	Flags       uint32   `json:"f"` // 环境异常位，见 Flag* 常量
	Behavior    Behavior `json:"b"`
}

// Behavior 是压缩后的人机交互特征。
type Behavior struct {
	Moves        int   `json:"mv"` // 鼠标移动事件数
	Clicks       int   `json:"cl"`
	Keys         int   `json:"kd"`
	Scrolls      int   `json:"sc"`
	Touches      int   `json:"tc"`
	DwellMs      int64 `json:"dw"` // 页面停留毫秒
	Straightness int   `json:"st"` // 最近轨迹 直线距离/路径长度 ×1000，脚本轨迹趋近 1000
	SpeedCV      int   `json:"vc"` // 最近轨迹速度变异系数 ×1000，匀速脚本趋近 0
}

const refreshHeader = "X-Guard-Refresh"

type keyset struct {
	token cipher.AEAD
	sess  []byte
	pow   []byte
}

// Guard 是风控校验器，并发安全。
type Guard struct {
	store   kvstore.Store
	keys    []keyset // [0] 为当前密钥，其余仅用于校验（密钥轮换）
	cipher  Cipher
	ciphers map[string]Cipher // 含当前算法与 WithAcceptCiphers 的旧算法
	o       *options
	mode    atomic.Int32
}

// New 创建 Guard。masterKey 至少 32 字节，务必来自密钥管理系统且各实例一致。
func New(store kvstore.Store, masterKey []byte, opts ...Option) (*Guard, error) {
	if store == nil {
		return nil, errors.New("botguard: nil store")
	}
	o := defaults()
	for _, fn := range opts {
		fn(o)
	}
	if o.rotateAfter == 0 {
		o.rotateAfter = o.tokenTTL / 2
	}
	active, ciphers, err := collectCiphers(o)
	if err != nil {
		return nil, err
	}
	g := &Guard{store: store, o: o, cipher: active, ciphers: ciphers}
	for _, mk := range append([][]byte{masterKey}, o.previousKeys...) {
		ks, err := newKeyset(mk)
		if err != nil {
			return nil, err
		}
		g.keys = append(g.keys, ks)
	}
	g.mode.Store(int32(o.mode))
	return g, nil
}

func newKeyset(master []byte) (keyset, error) {
	if len(master) < 32 {
		return keyset{}, errors.New("botguard: master key must be at least 32 bytes")
	}
	block, err := aes.NewCipher(derive(master, "botguard/token"))
	if err != nil {
		return keyset{}, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return keyset{}, err
	}
	return keyset{token: aead, sess: derive(master, "botguard/session"), pow: derive(master, "botguard/pow")}, nil
}

func derive(key []byte, label string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(label))
	return mac.Sum(nil)
}

// SetMode 运行时切换模式。
func (g *Guard) SetMode(m Mode) { g.mode.Store(int32(m)) }

// Mode 返回当前模式。
func (g *Guard) Mode() Mode { return Mode(g.mode.Load()) }

type verdictKey struct{}

// FromContext 读取中间件写入的判定结果。
func FromContext(ctx context.Context) (*Verdict, bool) {
	v, ok := ctx.Value(verdictKey{}).(*Verdict)
	return v, ok
}

// Middleware 返回风控校验中间件。
func (g *Guard) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mode := g.Mode()
			if mode == ModeOff || shouldSkip(r.URL.Path, g.o.skipPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			v := g.evaluate(r, time.Now())
			if v.Failure == "" && v.TokenAge >= g.o.rotateAfter {
				w.Header().Set(refreshHeader, "1")
			}
			if g.o.onVerdict != nil {
				g.o.onVerdict(r, v)
			}
			r = r.WithContext(context.WithValue(r.Context(), verdictKey{}, v))

			if mode == ModeMonitor || v.Action == ActionAllow {
				next.ServeHTTP(w, r)
				return
			}
			switch v.Action {
			case ActionChallenge:
				if g.o.onChallenge != nil {
					g.o.onChallenge(w, r, v)
				} else {
					writeJSON(w, http.StatusPreconditionRequired, map[string]any{
						"code": "challenge",
						"pow":  g.NewChallenge(v.TokenID),
					})
				}
			case ActionDeceive:
				g.o.deceive.ServeHTTP(w, r)
			default:
				g.o.onReject(w, r, v)
			}
		})
	}
}

// evaluate 执行由快到慢的校验漏斗。会读取并重置 r.Body。
func (g *Guard) evaluate(r *http.Request, now time.Time) *Verdict {
	o := g.o
	ctx := r.Context()
	v := &Verdict{Action: ActionBlock}

	// 1. 令牌：解封、时效
	tok := r.Header.Get(o.tokenHeader)
	if tok == "" && o.tokenCookie != "" {
		if c, err := r.Cookie(o.tokenCookie); err == nil {
			tok = c.Value
		}
	}
	if tok == "" {
		v.Failure = FailTokenMissing
		return v
	}
	claims, ks, err := g.openToken(tok)
	if err != nil {
		v.Failure = FailTokenInvalid
		return v
	}
	v.TokenID = claims.ID
	if now.Unix() >= claims.ExpiresAt {
		v.Failure = FailTokenExpired
		return v
	}
	v.TokenAge = now.Sub(time.Unix(claims.IssuedAt, 0))

	// 2. 签名格式
	rawSig := r.Header.Get(o.signHeader)
	if rawSig == "" {
		v.Failure = FailSignMissing
		return v
	}
	id, sealed, err := decodeSign(rawSig)
	if err != nil {
		v.Failure = FailSignInvalid
		return v
	}
	alg, ok := g.ciphers[id]
	if !ok {
		v.Failure = FailSignInvalid
		return v
	}

	// 3. 解密并比对业务摘要（摘要作为 AAD，任何参数被改动都会解密失败）
	body, fail := readBody(r, o.maxBody)
	if fail != "" {
		v.Failure = fail
		return v
	}
	aad := CanonicalDigest(r.Method, r.URL.Path, r.URL.Query(), body)
	plain, err := alg.Open(sessionKey(ks.sess, claims.ID), sealed, aad)
	if err != nil {
		v.Failure = FailSignInvalid
		return v
	}
	var p Payload
	if err := json.Unmarshal(plain, &p); err != nil || len(p.Nonce) < 8 || len(p.Nonce) > 64 || len(p.Fingerprint) > 128 {
		v.Failure = FailSignInvalid
		return v
	}

	// 4. 时间窗口 + Nonce 唯一
	if abs64(now.UnixMilli()-p.TS) > o.window.Milliseconds() {
		v.Failure = FailTimestamp
		return v
	}
	fresh, err := g.store.SetNX(ctx, "bg:n:"+claims.ID+":"+p.Nonce, []byte{1}, 2*o.window)
	if err != nil {
		v.Failure = FailStore
		return v
	}
	if !fresh {
		v.Failure = FailReplay
		return v
	}

	// 5. 环境风险评估
	ip := o.clientIP(r)
	s := &Signals{
		Flags:               p.Flags,
		Behavior:            p.Behavior,
		Fingerprint:         p.Fingerprint,
		UserAgent:           r.UserAgent(),
		IP:                  ip,
		FingerprintMismatch: claims.Fingerprint != "" && claims.Fingerprint != p.Fingerprint,
		IPMismatch:          claims.IPNet != "" && claims.IPNet != ipPrefix(ip),
		TokenAge:            v.TokenAge,
	}
	if o.rateLimit > 0 {
		key := p.Fingerprint
		if key == "" {
			key = "t:" + claims.ID
		}
		n, err := g.store.Incr(ctx, "bg:r:"+key+":"+strconv.FormatInt(now.Unix()/60, 10), 1, 2*time.Minute)
		if err == nil {
			s.RecentRequests = n
			s.RateLimited = n > int64(o.rateLimit)
		}
	}
	v.Signals = s
	for _, rule := range o.rules {
		if rule.Match(s) {
			v.Score += rule.Score
			v.Reasons = append(v.Reasons, rule.Name)
		}
	}

	switch {
	case v.Score >= o.blockAt:
		if o.deceive != nil {
			v.Action = ActionDeceive
		}
	case v.Score >= o.challengeAt:
		v.Action = ActionChallenge
		if g.challengeCleared(r, v, now) {
			v.Action = ActionAllow
			v.Cleared = true
		}
	default:
		v.Action = ActionAllow
	}
	return v
}

// challengeCleared 判断该令牌是否已通过挑战（之前通过 / 本次携带有效 PoW / 外部验证码）。
func (g *Guard) challengeCleared(r *http.Request, v *Verdict, now time.Time) bool {
	ctx := r.Context()
	key := "bg:cp:" + v.TokenID
	if _, ok, err := g.store.Get(ctx, key); err == nil && ok {
		return true
	}
	passed := false
	if h := r.Header.Get(g.o.powHeader); h != "" {
		passed = g.verifyPoW(ctx, h, v.TokenID, now)
	}
	if !passed && g.o.challengePass != nil {
		passed = g.o.challengePass(r, v)
	}
	if passed {
		_ = g.store.Set(ctx, key, []byte{1}, g.o.passTTL)
	}
	return passed
}

func readBody(r *http.Request, limit int64) ([]byte, string) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, ""
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	_ = r.Body.Close()
	if err != nil {
		return nil, FailBodyRead
	}
	if int64(len(body)) > limit {
		return nil, FailBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, ""
}

func defaultClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func defaultReject(w http.ResponseWriter, _ *http.Request, v *Verdict) {
	switch v.Failure {
	case FailTokenMissing, FailTokenInvalid, FailTokenExpired, FailTimestamp:
		// SDK 收到后会刷新令牌（同时校准时钟）并重试
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "token_required"})
	case FailStore:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
	default:
		// 统一模糊响应，不向攻击者暴露命中了哪条规则
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "denied"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
