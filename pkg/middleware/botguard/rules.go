package botguard

import (
	"strings"
	"time"
)

// 浏览器 SDK 上报的环境异常位（Payload.Flags），数值与 botguard.js 保持一致。
const (
	FlagWebdriver       uint32 = 1 << iota // navigator.webdriver 为 true
	FlagHeadlessUA                         // UA 含 HeadlessChrome
	FlagNoChromeRuntime                    // UA 声称 Chrome 但缺失 window.chrome
	FlagNativeTampered                     // 原生函数被替换 / navigator 上有自有属性（Proxy、补环境）
	FlagZeroWindow                         // outerWidth / outerHeight 为 0
	FlagNoPlugins                          // navigator.plugins 为空
	FlagNoLanguages                        // navigator.languages 为空
	FlagCanvasBlocked                      // Canvas 不可用或被屏蔽
	FlagSoftwareGL                         // WebGL 为软件渲染（SwiftShader / llvmpipe）
)

// Signals 是供规则评估的风险信号。
type Signals struct {
	Flags       uint32
	Behavior    Behavior
	Fingerprint string
	UserAgent   string
	IP          string
	// FingerprintMismatch 签名中的指纹与令牌签发时绑定的指纹不一致（令牌被转移到其他设备）。
	FingerprintMismatch bool
	// IPMismatch 当前 IP 与令牌签发时不在同一网段。移动网络下较常见，宜低权重。
	IPMismatch bool
	TokenAge   time.Duration
	// RecentRequests 当前指纹（无指纹时按令牌）本分钟内的请求数。
	RecentRequests int64
	RateLimited    bool
}

// Has 判断是否带有某个环境异常位。
func (s *Signals) Has(flag uint32) bool { return s.Flags&flag != 0 }

// Rule 是一条风险规则，命中则累加 Score。
type Rule struct {
	Name  string
	Score int
	Match func(*Signals) bool
}

func hasFlag(f uint32) func(*Signals) bool {
	return func(s *Signals) bool { return s.Has(f) }
}

// DefaultRules 返回默认规则集。分值配合默认阈值（挑战 30 / 拦截 70）设计：
// 单个弱信号只记分不处置，多个信号叠加或单个强信号才触发挑战或拦截。
func DefaultRules() []Rule {
	return []Rule{
		{Name: "webdriver", Score: 60, Match: hasFlag(FlagWebdriver)},
		{Name: "bot_user_agent", Score: 60, Match: func(s *Signals) bool { return IsBotUserAgent(s.UserAgent) }},
		{Name: "headless_ua", Score: 50, Match: hasFlag(FlagHeadlessUA)},
		{Name: "native_tampered", Score: 40, Match: hasFlag(FlagNativeTampered)},
		{Name: "fingerprint_mismatch", Score: 40, Match: func(s *Signals) bool { return s.FingerprintMismatch }},
		{Name: "zero_window", Score: 30, Match: hasFlag(FlagZeroWindow)},
		{Name: "rate_exceeded", Score: 30, Match: func(s *Signals) bool { return s.RateLimited }},
		{Name: "no_chrome_runtime", Score: 25, Match: hasFlag(FlagNoChromeRuntime)},
		{Name: "linear_trajectory", Score: 25, Match: func(s *Signals) bool {
			return s.Behavior.Moves >= 20 && s.Behavior.Straightness >= 990
		}},
		{Name: "software_gl", Score: 20, Match: hasFlag(FlagSoftwareGL)},
		{Name: "no_interaction", Score: 20, Match: func(s *Signals) bool {
			b := s.Behavior
			return b.Moves+b.Clicks+b.Keys+b.Scrolls+b.Touches == 0 && b.DwellMs >= 10_000
		}},
		{Name: "constant_speed", Score: 15, Match: func(s *Signals) bool {
			return s.Behavior.Moves >= 20 && s.Behavior.SpeedCV < 50
		}},
		{Name: "no_plugins_languages", Score: 15, Match: func(s *Signals) bool {
			return s.Has(FlagNoPlugins) && s.Has(FlagNoLanguages)
		}},
		{Name: "canvas_blocked", Score: 10, Match: hasFlag(FlagCanvasBlocked)},
		{Name: "ip_changed", Score: 10, Match: func(s *Signals) bool { return s.IPMismatch }},
	}
}

var botUAMarkers = []string{
	"headless", "phantomjs", "selenium", "puppeteer", "playwright", "scrapy",
	"python-requests", "python-urllib", "aiohttp", "httpx", "curl/", "wget/",
	"go-http-client", "java/", "apache-httpclient", "node-fetch", "undici", "axios/",
	"libwww-perl", "bot/", "spider", "crawler",
}

// IsBotUserAgent 判断 UA 是否为空或命中常见自动化 / 爬虫库标识。服务端可独立判断，不依赖 SDK 上报。
func IsBotUserAgent(ua string) bool {
	if ua == "" {
		return true
	}
	ua = strings.ToLower(ua)
	for _, m := range botUAMarkers {
		if strings.Contains(ua, m) {
			return true
		}
	}
	return false
}
