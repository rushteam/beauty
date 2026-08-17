package llm

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"time"
)

// Fallback 按顺序尝试多个 client,前一个出错就换下一个(Generate 与 Stream 均适用)。
// 用于跨 provider/模型的高可用:主用一家、挂了自动切备用。
func Fallback(clients ...Client) Client {
	return &fallback{clients: clients}
}

type fallback struct{ clients []Client }

func (f *fallback) Generate(ctx context.Context, req Request) (*Response, error) {
	if len(f.clients) == 0 {
		return nil, ErrNoClients
	}
	var lastErr error
	for _, c := range f.clients {
		resp, err := c.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil { // 调用方取消:不再试其它家
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (f *fallback) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		if len(f.clients) == 0 {
			yield(Chunk{}, ErrNoClients)
			return
		}
		var lastErr error
		for _, c := range f.clients {
			started := false
			var setupErr error
			for chunk, err := range c.Stream(ctx, req) {
				if err != nil {
					if started {
						// 已产出增量,不可切换,直接透传
						yield(chunk, err)
						return
					}
					// 建流失败:尝试下一家
					setupErr = err
					break
				}
				started = true
				if !yield(chunk, nil) {
					return
				}
			}
			if started || setupErr == nil {
				// 已产出增量,或空流正常结束——均视为成功,不再切换
				return
			}
			lastErr = setupErr
			if ctx.Err() != nil {
				yield(Chunk{}, ctx.Err())
				return
			}
		}
		if lastErr != nil {
			yield(Chunk{}, lastErr)
		}
	}
}

// ErrorKind 分类错误类型,用于 FallbackConfig 路由到不同的降级链。
type ErrorKind int

const (
	ErrorGeneral         ErrorKind = iota // 通用错误
	ErrorRateLimit                        // 速率限制 (429, quota exceeded)
	ErrorContextOverflow                  // 上下文窗口溢出 (context_length_exceeded)
	ErrorMaxOutput                        // 输出 token 上限 (max_output_tokens)
)

// ErrorWithKind 允许 provider 返回已分类的错误。
type ErrorWithKind interface {
	error
	ErrorKind() ErrorKind
}

// ClassifyError 判断错误属于哪种 ErrorKind。
// 优先通过 ErrorWithKind;否则按错误消息关键词(不区分大小写)匹配。
func ClassifyError(err error) ErrorKind {
	if err == nil {
		return ErrorGeneral
	}
	var ek ErrorWithKind
	if errors.As(err, &ek) {
		return ek.ErrorKind()
	}
	msg := strings.ToLower(err.Error())
	rateLimitKeys := []string{
		"rate limit", "rate_limit", "quota exceeded", "quota_exceeded",
		"too many requests", "throttl",
	}
	for _, k := range rateLimitKeys {
		if strings.Contains(msg, k) {
			return ErrorRateLimit
		}
	}
	// "429" 必须作为独立数字匹配,避免 "1429"/"4290" 误伤
	if containsStandaloneNumber(msg, "429") {
		return ErrorRateLimit
	}
	outputKeys := []string{
		"max_output_tokens", "max output tokens", "output token",
		"completion_tokens", "output length exceeded",
	}
	for _, k := range outputKeys {
		if strings.Contains(msg, k) {
			return ErrorMaxOutput
		}
	}
	contextKeys := []string{
		"context_length", "context length", "maximum context", "token limit",
		"too many tokens", "input too long", "prompt_too_long", "prompt too long",
	}
	for _, k := range contextKeys {
		if strings.Contains(msg, k) {
			return ErrorContextOverflow
		}
	}
	// max_tokens 单独处理:仅当伴随超限语义时判为溢出(避免 "max_tokens parameter invalid")
	if strings.Contains(msg, "max_tokens") &&
		(strings.Contains(msg, "exceed") || strings.Contains(msg, "overflow") ||
			strings.Contains(msg, "too large") || strings.Contains(msg, "too long")) {
		return ErrorContextOverflow
	}
	return ErrorGeneral
}

// containsStandaloneNumber 判断 s 中是否出现独立数字 token(前后非数字)。
func containsStandaloneNumber(s, num string) bool {
	for i := 0; i+len(num) <= len(s); i++ {
		if s[i:i+len(num)] != num {
			continue
		}
		beforeOK := i == 0 || s[i-1] < '0' || s[i-1] > '9'
		after := i + len(num)
		afterOK := after == len(s) || s[after] < '0' || s[after] > '9'
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

// FallbackConfig 按错误类型路由到不同的降级 Client 链。
// 比 Fallback 更精确:速率限制换更大配额的 provider;上下文溢出换更大窗口的模型;
// 401/403 配置错误不会被错误地降级。
type FallbackConfig struct {
	Primary           Client // 主 client(必填)
	OnRateLimit       []Client
	OnContextOverflow []Client
	OnError           []Client // 其他错误时的降级链(兜底)
	// OnFallback 在发生降级时回调(可选),用于监控/告警。
	OnFallback func(ctx context.Context, kind ErrorKind, primary, fallback string, err error)
}

// Build 构建一个实现 Client 接口的降级 client。
func (cfg FallbackConfig) Build() Client {
	return &fallbackConfig{cfg: cfg}
}

type fallbackConfig struct{ cfg FallbackConfig }

func (f *fallbackConfig) chainFor(kind ErrorKind) []Client {
	switch kind {
	case ErrorRateLimit:
		if len(f.cfg.OnRateLimit) > 0 {
			return f.cfg.OnRateLimit
		}
	case ErrorContextOverflow:
		if len(f.cfg.OnContextOverflow) > 0 {
			return f.cfg.OnContextOverflow
		}
	}
	return f.cfg.OnError
}

func clientLabel(c Client) string {
	if c == nil {
		return ""
	}
	type stringer interface{ String() string }
	if s, ok := c.(stringer); ok {
		return s.String()
	}
	return fmt.Sprintf("%T", c)
}

func (f *fallbackConfig) Generate(ctx context.Context, req Request) (*Response, error) {
	if f.cfg.Primary == nil {
		return nil, ErrNoClients
	}
	resp, err := f.cfg.Primary.Generate(ctx, req)
	if err == nil {
		return resp, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	kind := ClassifyError(err)
	chain := f.chainFor(kind)
	if len(chain) == 0 {
		return nil, err
	}
	lastErr := err
	primaryName := clientLabel(f.cfg.Primary)
	for _, c := range chain {
		if f.cfg.OnFallback != nil {
			f.cfg.OnFallback(ctx, kind, primaryName, clientLabel(c), err)
		}
		resp, tryErr := c.Generate(ctx, req)
		if tryErr == nil {
			return resp, nil
		}
		lastErr = tryErr
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (f *fallbackConfig) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		if f.cfg.Primary == nil {
			yield(Chunk{}, ErrNoClients)
			return
		}
		done, setupErr := f.tryStream(ctx, req, f.cfg.Primary, yield)
		if done {
			return
		}
		if ctx.Err() != nil {
			yield(Chunk{}, ctx.Err())
			return
		}
		// setupErr == nil 且 !done 不应出现;防御性直接返回
		if setupErr == nil {
			return
		}
		kind := ClassifyError(setupErr)
		chain := f.chainFor(kind)
		if len(chain) == 0 {
			yield(Chunk{}, setupErr)
			return
		}
		lastErr := setupErr
		primaryName := clientLabel(f.cfg.Primary)
		for _, c := range chain {
			if f.cfg.OnFallback != nil {
				f.cfg.OnFallback(ctx, kind, primaryName, clientLabel(c), setupErr)
			}
			done, err := f.tryStream(ctx, req, c, yield)
			if done {
				return
			}
			if err != nil {
				lastErr = err
			}
			if ctx.Err() != nil {
				yield(Chunk{}, ctx.Err())
				return
			}
		}
		if lastErr != nil {
			yield(Chunk{}, lastErr)
		}
	}
}

// tryStream 尝试单个 client 的 Stream。
// done=true: 流已正常结束(含空流)或中途错误已透传,调用方不应再降级;
// done=false 且 setupErr!=nil: 建流/首包失败,可降级。
func (f *fallbackConfig) tryStream(ctx context.Context, req Request, c Client, yield func(Chunk, error) bool) (done bool, setupErr error) {
	started := false
	for chunk, err := range c.Stream(ctx, req) {
		if err != nil {
			if started {
				yield(chunk, err)
				return true, nil
			}
			return false, err
		}
		started = true
		if !yield(chunk, nil) {
			return true, nil
		}
	}
	// 正常结束(可能零 chunk)——视为成功,勿降级
	return true, nil
}

// Retry 对 Generate/Stream 的**建立阶段**错误重试至多 attempts 次。
// 退避策略:指数退避 + 随机 jitter(delay * 2^i * [0.5,1.5)),防止雷群效应。
// Stream 一旦开始产出就不重试(已消费的增量无法回滚),仅重试建流失败。
func Retry(c Client, attempts int, delay time.Duration) Client {
	if attempts < 1 {
		attempts = 1
	}
	return &retry{c: c, attempts: attempts, delay: delay}
}

type retry struct {
	c        Client
	attempts int
	delay    time.Duration
}

func (r *retry) backoff(ctx context.Context, i int) bool {
	if i >= r.attempts-1 {
		return false
	}
	base := float64(r.delay) * math.Pow(2, float64(i))
	jitter := 0.5 + rand.Float64() // [0.5, 1.5)
	wait := time.Duration(base * jitter)
	select {
	case <-time.After(wait):
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *retry) Generate(ctx context.Context, req Request) (*Response, error) {
	var lastErr error
	for i := 0; i < r.attempts; i++ {
		resp, err := r.c.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !r.backoff(ctx, i) {
			break
		}
	}
	return nil, lastErr
}

func (r *retry) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		var lastErr error
		for i := 0; i < r.attempts; i++ {
			started := false
			var setupErr error
			for chunk, err := range r.c.Stream(ctx, req) {
				if err != nil {
					if started {
						// 已产出增量,不可回滚,直接透传
						yield(chunk, err)
						return
					}
					// 建流失败:记录错误并重试,勿在此处 yield(否则会吞掉后续重试机会)
					setupErr = err
					break
				}
				started = true
				if !yield(chunk, nil) {
					return
				}
			}
			if started || setupErr == nil {
				// 已产出增量,或空流正常结束——均视为成功,不再重试
				return
			}
			lastErr = setupErr
			if !r.backoff(ctx, i) {
				break
			}
		}
		if lastErr != nil {
			yield(Chunk{}, lastErr)
			return
		}
		if err := ctx.Err(); err != nil {
			yield(Chunk{}, err)
		}
	}
}

// UsageHook 在一次生成完成后收到用量与耗时,用于计量/计费/埋点(接 OTel、日志、账单等由你定)。
type UsageHook func(ctx context.Context, model string, u Usage, latency time.Duration)

// Metered 包一层 client,在 Generate/Stream 结束后回调 hook 上报用量与延迟。
// 流式场景在迭代结束后回调,累计最终 Usage。
func Metered(c Client, hook UsageHook) Client {
	return &metered{c: c, hook: hook}
}

type metered struct {
	c    Client
	hook UsageHook
}

func (m *metered) Generate(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()
	resp, err := m.c.Generate(ctx, req)
	if err == nil && m.hook != nil {
		m.hook(ctx, resp.Model, resp.Usage, time.Since(start))
	}
	return resp, err
}

func (m *metered) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		start := time.Now()
		var usage Usage
		for chunk, err := range m.c.Stream(ctx, req) {
			if err != nil {
				yield(chunk, err)
				return
			}
			if chunk.Usage != nil {
				usage = *chunk.Usage
			}
			if !yield(chunk, nil) {
				if m.hook != nil {
					m.hook(ctx, req.Model, usage, time.Since(start))
				}
				return
			}
		}
		if m.hook != nil {
			m.hook(ctx, req.Model, usage, time.Since(start))
		}
	}
}

// ErrBudgetExceeded 表示累计 token 用量超出 Budget 设定的上限。
var ErrBudgetExceeded = errors.New("llm: token budget exceeded")

// Budget 为一个 Client 加累计 token 用量上限(input+output 合计)。超限后所有请求立即返回
// ErrBudgetExceeded。用于开发/测试/按用户限额等场景。并发安全。
func Budget(c Client, maxTokens int64) *BudgetClient {
	return &BudgetClient{c: c, max: maxTokens}
}

// BudgetClient 是带 token 预算的 Client 包装。
type BudgetClient struct {
	c    Client
	max  int64
	used int64
}

// Used 返回已消耗的累计 token 数。
func (b *BudgetClient) Used() int64 { return atomic.LoadInt64(&b.used) }

// Remaining 返回剩余可用 token 数。
func (b *BudgetClient) Remaining() int64 {
	r := b.max - atomic.LoadInt64(&b.used)
	if r < 0 {
		return 0
	}
	return r
}

// Reset 重置已消耗计数为 0。
func (b *BudgetClient) Reset() { atomic.StoreInt64(&b.used, 0) }

func (b *BudgetClient) add(u Usage) error {
	tokens := int64(u.InputTokens + u.OutputTokens)
	if tokens <= 0 {
		return nil
	}
	for {
		used := atomic.LoadInt64(&b.used)
		if used >= b.max {
			return ErrBudgetExceeded
		}
		if used+tokens > b.max {
			return ErrBudgetExceeded
		}
		if atomic.CompareAndSwapInt64(&b.used, used, used+tokens) {
			return nil
		}
	}
}

func (b *BudgetClient) check() error {
	if atomic.LoadInt64(&b.used) >= b.max {
		return ErrBudgetExceeded
	}
	return nil
}

func (b *BudgetClient) Generate(ctx context.Context, req Request) (*Response, error) {
	if err := b.check(); err != nil {
		return nil, err
	}
	resp, err := b.c.Generate(ctx, req)
	if err == nil {
		if addErr := b.add(resp.Usage); addErr != nil {
			return resp, addErr
		}
	}
	return resp, err
}

func (b *BudgetClient) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	return func(yield func(Chunk, error) bool) {
		if err := b.check(); err != nil {
			yield(Chunk{}, err)
			return
		}
		for chunk, err := range b.c.Stream(ctx, req) {
			if err != nil {
				yield(chunk, err)
				return
			}
			if chunk.Usage != nil {
				if err := b.add(*chunk.Usage); err != nil {
					yield(Chunk{}, err)
					return
				}
			}
			if !yield(chunk, nil) {
				return
			}
		}
	}
}
