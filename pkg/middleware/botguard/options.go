package botguard

import (
	"net/http"
	"time"
)

type options struct {
	mode         Mode
	previousKeys [][]byte

	tokenTTL    time.Duration
	rotateAfter time.Duration
	window      time.Duration
	maxBody     int64

	tokenHeader string
	tokenCookie string
	signHeader  string
	powHeader   string

	skipPrefixes []string
	clientIP     func(*http.Request) string

	rules       []Rule
	challengeAt int
	blockAt     int
	rateLimit   int

	powBits int
	powTTL  time.Duration
	passTTL time.Duration

	cipher Cipher
	accept []Cipher

	onVerdict     func(*http.Request, *Verdict)
	onChallenge   func(http.ResponseWriter, *http.Request, *Verdict)
	onReject      func(http.ResponseWriter, *http.Request, *Verdict)
	challengePass func(*http.Request, *Verdict) bool
	deceive       http.Handler
}

func defaults() *options {
	return &options{
		tokenTTL:    30 * time.Minute,
		window:      60 * time.Second,
		maxBody:     1 << 20,
		tokenHeader: "X-Guard-Token",
		tokenCookie: "bg_token",
		signHeader:  "X-Guard-Sign",
		powHeader:   "X-Guard-PoW",
		clientIP:    defaultClientIP,
		rules:       DefaultRules(),
		challengeAt: 30,
		blockAt:     70,
		rateLimit:   120,
		powBits:     16,
		powTTL:      2 * time.Minute,
		passTTL:     10 * time.Minute,
		onReject:    defaultReject,
	}
}

// Option 配置 Guard。
type Option func(*options)

// WithCipher 设置当前签名算法，用于签发和校验。默认 AESGCM（id "v1"）。
// 浏览器需 BotGuard.registerCipher 注册同一 id，并在 init({ cipher: id }) 中启用。
func WithCipher(c Cipher) Option { return func(o *options) { o.cipher = c } }

// WithAcceptCiphers 追加只用于校验的旧算法，便于算法轮换。当前算法已经接受，无需重复传入。
func WithAcceptCiphers(cs ...Cipher) Option {
	return func(o *options) { o.accept = append(o.accept, cs...) }
}

// WithMode 设置初始模式，默认 ModeEnforce。
func WithMode(m Mode) Option { return func(o *options) { o.mode = m } }

// WithPreviousKeys 追加旧主密钥，仅用于校验已签发的令牌/挑战，便于平滑轮换。
func WithPreviousKeys(keys ...[]byte) Option {
	return func(o *options) { o.previousKeys = append(o.previousKeys, keys...) }
}

// WithTokenTTL 令牌有效期，默认 30 分钟。
func WithTokenTTL(d time.Duration) Option { return func(o *options) { o.tokenTTL = d } }

// WithRotateAfter 令牌签发超过该时长后，响应头带 X-Guard-Refresh 提示 SDK 后台轮换。默认 TTL/2。
func WithRotateAfter(d time.Duration) Option { return func(o *options) { o.rotateAfter = d } }

// WithWindow 签名时间戳允许的偏差，默认 60 秒；Nonce 保留 2 倍窗口。
func WithWindow(d time.Duration) Option { return func(o *options) { o.window = d } }

// WithMaxBody 参与摘要的请求体上限，默认 1MiB，超过直接拒绝。
func WithMaxBody(n int64) Option { return func(o *options) { o.maxBody = n } }

// WithHeaders 自定义令牌 / 签名 / PoW 的 header 名称，空串表示保持默认。需与 SDK init 参数一致。
func WithHeaders(token, sign, pow string) Option {
	return func(o *options) {
		if token != "" {
			o.tokenHeader = token
		}
		if sign != "" {
			o.signHeader = sign
		}
		if pow != "" {
			o.powHeader = pow
		}
	}
}

// WithTokenCookie 令牌 header 缺失时回退读取的 cookie 名，默认 "bg_token"，空串关闭。
func WithTokenCookie(name string) Option { return func(o *options) { o.tokenCookie = name } }

// WithSkipPrefixes 跳过校验的路径前缀。
func WithSkipPrefixes(prefixes ...string) Option {
	return func(o *options) { o.skipPrefixes = append(o.skipPrefixes, prefixes...) }
}

// WithClientIP 自定义客户端 IP 提取（如信任网关的 X-Forwarded-For），默认取 RemoteAddr。
func WithClientIP(fn func(*http.Request) string) Option {
	return func(o *options) { o.clientIP = fn }
}

// WithRules 替换风险规则集，默认 DefaultRules()。可 append(botguard.DefaultRules(), myRule...)。
func WithRules(rules ...Rule) Option { return func(o *options) { o.rules = rules } }

// WithThresholds 设置风险分阈值：>= challenge 触发挑战，>= block 拦截/蜜罐。默认 30 / 70。
func WithThresholds(challenge, block int) Option {
	return func(o *options) { o.challengeAt, o.blockAt = challenge, block }
}

// WithFingerprintRateLimit 单指纹每分钟请求数上限，超过命中 rate_exceeded 规则。默认 120，0 关闭。
func WithFingerprintRateLimit(perMinute int) Option {
	return func(o *options) { o.rateLimit = perMinute }
}

// WithPoW 设置工作量证明难度（前导零比特数，默认 16，约 6.5 万次哈希）与挑战有效期。
func WithPoW(bits int, ttl time.Duration) Option {
	return func(o *options) { o.powBits, o.powTTL = bits, ttl }
}

// WithChallengePassTTL 通过挑战后该令牌免挑战的时长，默认 10 分钟。
func WithChallengePassTTL(d time.Duration) Option { return func(o *options) { o.passTTL = d } }

// WithOnVerdict 每次判定后回调，用于日志、指标、离线分析。不要在其中阻塞。
func WithOnVerdict(fn func(*http.Request, *Verdict)) Option {
	return func(o *options) { o.onVerdict = fn }
}

// WithChallengeHandler 自定义挑战响应（如改为下发滑块验证码）。默认返回 428 + PoW 挑战。
func WithChallengeHandler(fn func(http.ResponseWriter, *http.Request, *Verdict)) Option {
	return func(o *options) { o.onChallenge = fn }
}

// WithChallengePass 外部挑战（验证码等）是否已通过，如校验请求中的验证码票据。
// 返回 true 时视同通过挑战，令牌在 ChallengePassTTL 内免挑战。
func WithChallengePass(fn func(*http.Request, *Verdict) bool) Option {
	return func(o *options) { o.challengePass = fn }
}

// WithDeceive 高风险请求交给蜜罐 handler 返回假数据，而不是直接拦截，避免攻击者通过报错盲猜规则。
func WithDeceive(h http.Handler) Option { return func(o *options) { o.deceive = h } }

// WithRejectHandler 自定义拦截响应。Verdict.Failure 为空表示是风险分拦截。
func WithRejectHandler(fn func(http.ResponseWriter, *http.Request, *Verdict)) Option {
	return func(o *options) { o.onReject = fn }
}
