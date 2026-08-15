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
		for i, c := range f.clients {
			started := false
			for chunk, err := range c.Stream(ctx, req) {
				started = true
				if err != nil {
					if i < len(f.clients)-1 {
						break
					}
					yield(chunk, err)
					return
				}
				if !yield(chunk, nil) {
					return
				}
			}
			if started {
				return
			}
			if ctx.Err() != nil {
				yield(Chunk{}, ctx.Err())
				return
			}
		}
	}
}

// ErrorKind 分类错误类型,用于 FallbackConfig 路由到不同的降级链。
type ErrorKind int

const (
	ErrorGeneral         ErrorKind = iota // 通用错误
	ErrorRateLimit                        // 速率限制 (429, quota exceeded)
	ErrorContextOverflow                  // 上下文窗口溢出 (context_length_exceeded)
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
		"rate limit", "rate_limit", "429", "quota", "too many requests", "throttl",
	}
	for _, k := range rateLimitKeys {
		if strings.Contains(msg, k) {
			return ErrorRateLimit
		}
	}
	contextKeys := []string{
		"context_length", "context length", "maximum context", "token limit",
		"too many tokens", "max_tokens", "input too long",
	}
	for _, k := range contextKeys {
		if strings.Contains(msg, k) {
			return ErrorContextOverflow
		}
	}
	return ErrorGeneral
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
		started, setupErr := f.tryStream(ctx, req, f.cfg.Primary, yield)
		if started {
			return
		}
		if ctx.Err() != nil {
			yield(Chunk{}, ctx.Err())
			return
		}
		kind := ClassifyError(setupErr)
		chain := f.chainFor(kind)
		if len(chain) == 0 {
			if setupErr != nil {
				yield(Chunk{}, setupErr)
			}
			return
		}
		lastErr := setupErr
		primaryName := clientLabel(f.cfg.Primary)
		for _, c := range chain {
			if f.cfg.OnFallback != nil {
				f.cfg.OnFallback(ctx, kind, primaryName, clientLabel(c), setupErr)
			}
			started, err := f.tryStream(ctx, req, c, yield)
			if started {
				return
			}
			lastErr = err
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

// tryStream 尝试单个 client 的 Stream。started 表示已产出有效 chunk;否则 setupErr 为建流/首包错误(可降级)。
func (f *fallbackConfig) tryStream(ctx context.Context, req Request, c Client, yield func(Chunk, error) bool) (started bool, setupErr error) {
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
	return started, nil
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
			for chunk, err := range r.c.Stream(ctx, req) {
				started = true
				if err != nil {
					yield(chunk, err)
					return
				}
				if !yield(chunk, nil) {
					return
				}
			}
			if started {
				return
			}
			if !r.backoff(ctx, i) {
				break
			}
		}
		if lastErr != nil {
			yield(Chunk{}, lastErr)
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
