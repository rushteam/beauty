package llm

import (
	"context"
	"fmt"
	"iter"
	"regexp"
	"strings"
)

// Check 检查一次请求的输入,返回非 nil 即拦截(下游 client 不会被调用)。
// 约定返回 *GuardError 以携带触发的检查名与原因;返回普通 error 也会被 Guard 透传。
type Check func(ctx context.Context, req Request) error

// GuardError 表示某个护栏检查拦截了请求。Check 是检查名,Reason 是可读原因。
type GuardError struct {
	Check  string
	Reason string
}

func (e *GuardError) Error() string {
	return fmt.Sprintf("llm: blocked by guardrail %q: %s", e.Check, e.Reason)
}

// Guard 包一层 client:在 Generate/Stream 调用下游前,依次跑 checks,任一返回错误即拦截并返回该错误。
// 与 Fallback/Retry/Metered 一样是中间件,可任意叠加;被 Guard 的 client 交给 llm/agent.Runner 时,
// 工具循环里的每个模型回合都会先过输入检查。
//
// 内置的 check(PromptInjection/PII/MaxInputLen)的匹配规则是 policy,可用参数覆盖或自写 Check。
func Guard(c Client, checks ...Check) Client {
	return &guard{c: c, checks: checks}
}

type guard struct {
	c      Client
	checks []Check
}

func (g *guard) run(ctx context.Context, req Request) error {
	for _, chk := range g.checks {
		if err := chk(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

func (g *guard) Generate(ctx context.Context, req Request) (*Response, error) {
	if err := g.run(ctx, req); err != nil {
		return nil, err
	}
	return g.c.Generate(ctx, req)
}

func (g *guard) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		if err := g.run(ctx, req); err != nil {
			yield(Chunk{}, err)
			return
		}
		for chunk, err := range g.c.Stream(ctx, req) {
			if !yield(chunk, err) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

// inputText 汇总请求里"用户可控"的文本(System 与工具结果不算,以免误伤系统提示/工具返回)。
// 检查 user 消息与 assistant 文本足以覆盖注入/PII 场景;需要更宽可自写 Check 遍历全部消息。
func inputText(req Request) string {
	var sb strings.Builder
	for _, m := range req.Messages {
		if m.Role == User || m.Role == Assistant {
			sb.WriteString(m.Content)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// 默认的越狱/提示注入关键词表(policy——可用 PromptInjection(自定义...) 覆盖)。
var defaultInjectionPatterns = []string{
	"ignore previous instructions",
	"ignore your instructions",
	"forget everything above",
	"forget everything",
	"disregard guidelines",
	"developer mode",
	"override safety",
	"bypass restrictions",
	"ignore safeguards",
	"admin override",
	"root access",
	"jailbreak",
	"you are now a",
	"pretend you are",
	"act as if",
	"roleplay as",
	"simulate being",
	"忽略以上",
	"忽略之前的指令",
	"忽略先前的指令",
	"越狱",
}

// PromptInjection 拦截常见越狱/提示注入措辞(大小写不敏感)。不传参用内置词表;
// 传入 patterns 则用你的词表覆盖(整表替换,不是追加)。
func PromptInjection(patterns ...string) Check {
	pats := patterns
	if len(pats) == 0 {
		pats = defaultInjectionPatterns
	}
	lower := make([]string, len(pats))
	for i, p := range pats {
		lower[i] = strings.ToLower(p)
	}
	return func(_ context.Context, req Request) error {
		text := strings.ToLower(inputText(req))
		for i, p := range lower {
			if strings.Contains(text, p) {
				return &GuardError{Check: "prompt_injection", Reason: "命中注入措辞: " + pats[i]}
			}
		}
		return nil
	}
}

// 默认 PII 正则(policy):邮箱、含分隔的长数字串(卡号/手机号粗匹配)。宁可宽松,精确规则由使用方定。
var defaultPIIPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`), // email
	regexp.MustCompile(`\b(?:\d[ \-]?){13,19}\b`),                          // 银行卡号(13-19 位,允许空格/连字符)
	regexp.MustCompile(`\b1[3-9]\d{9}\b`),                                  // 中国大陆手机号
}

// PII 拦截疑似个人敏感信息(默认邮箱/卡号/手机号)。不传参用内置正则;传入 res 则整表替换。
func PII(res ...*regexp.Regexp) Check {
	pats := res
	if len(pats) == 0 {
		pats = defaultPIIPatterns
	}
	return func(_ context.Context, req Request) error {
		text := inputText(req)
		for _, re := range pats {
			if re.MatchString(text) {
				return &GuardError{Check: "pii", Reason: "疑似命中敏感信息: " + re.String()}
			}
		}
		return nil
	}
}

// MaxInputLen 限制输入文本总长度(rune 数),超出即拦截。用于挡超长/刷量输入。
func MaxInputLen(n int) Check {
	return func(_ context.Context, req Request) error {
		if l := len([]rune(inputText(req))); l > n {
			return &GuardError{Check: "max_input_len", Reason: fmt.Sprintf("输入 %d 字符超过上限 %d", l, n)}
		}
		return nil
	}
}

// ---- 输出护栏(Output Guard) ----

// OutputCheck 检查模型回复(content 为模型输出文本)。返回非 nil 即视为拦截。
type OutputCheck func(ctx context.Context, content string) error

// GuardOutput 包一层 client:Generate/Stream 结束后检查模型输出,任一 OutputCheck 命中则
// 返回错误(Generate 返 error;Stream 通过迭代器的 error 值返回)。与 Guard(输入)对称使用。
func GuardOutput(c Client, checks ...OutputCheck) Client {
	return &outputGuard{c: c, checks: checks}
}

type outputGuard struct {
	c      Client
	checks []OutputCheck
}

func (g *outputGuard) runChecks(ctx context.Context, content string) error {
	for _, chk := range g.checks {
		if err := chk(ctx, content); err != nil {
			return err
		}
	}
	return nil
}

func (g *outputGuard) Generate(ctx context.Context, req Request) (*Response, error) {
	resp, err := g.c.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := g.runChecks(ctx, resp.Content); err != nil {
		return nil, err
	}
	return resp, nil
}

func (g *outputGuard) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		var buf strings.Builder
		for chunk, err := range g.c.Stream(ctx, req) {
			if err != nil {
				yield(chunk, err)
				return
			}
			if chunk.Delta != "" {
				buf.WriteString(chunk.Delta)
			}
			if !yield(chunk, nil) {
				return
			}
		}
		if err := g.runChecks(ctx, buf.String()); err != nil {
			yield(Chunk{}, err)
		}
	}
}

// Toxic 拦截模型输出中的敏感/有害内容(大小写不敏感子串匹配)。
// 不传参用内置关键词;传入 patterns 整表替换。
func Toxic(patterns ...string) OutputCheck {
	pats := patterns
	if len(pats) == 0 {
		pats = defaultToxicPatterns
	}
	lower := make([]string, len(pats))
	for i, p := range pats {
		lower[i] = strings.ToLower(p)
	}
	return func(_ context.Context, content string) error {
		text := strings.ToLower(content)
		for i, p := range lower {
			if strings.Contains(text, p) {
				return &GuardError{Check: "toxic_output", Reason: "输出命中敏感词: " + pats[i]}
			}
		}
		return nil
	}
}

var defaultToxicPatterns = []string{
	"I cannot help with that",
	"I'm not able to assist",
	"as an AI language model",
}

// MaxOutputLen 限制模型输出长度(rune),超出即拦截。用于防止意外超长回复(成本/滥用)。
func MaxOutputLen(n int) OutputCheck {
	return func(_ context.Context, content string) error {
		if l := len([]rune(content)); l > n {
			return &GuardError{Check: "max_output_len", Reason: fmt.Sprintf("输出 %d 字符超过上限 %d", l, n)}
		}
		return nil
	}
}

// OutputRegexp 用正则匹配输出;命中即拦截。适合检查格式合规(如 JSON)或敏感模式。
func OutputRegexp(name string, re *regexp.Regexp) OutputCheck {
	return func(_ context.Context, content string) error {
		if re.MatchString(content) {
			return &GuardError{Check: name, Reason: "输出命中: " + re.String()}
		}
		return nil
	}
}
