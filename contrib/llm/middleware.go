package llm

import (
	"context"
	"errors"
	"iter"
	"math"
	"math/rand/v2"
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
