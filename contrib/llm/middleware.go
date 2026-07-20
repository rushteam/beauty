package llm

import (
	"context"
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

func (f *fallback) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	if len(f.clients) == 0 {
		return nil, ErrNoClients
	}
	var lastErr error
	for _, c := range f.clients {
		ch, err := c.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// Retry 对 Generate 的**建立阶段**错误重试至多 attempts 次,第 i 次失败后等 delay*(i+1)。
// 注意:Stream 一旦开始产出就不重试(已消费的增量无法回滚),仅重试建流失败。
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

func (r *retry) do(ctx context.Context, i int) bool {
	if i == r.attempts-1 {
		return false
	}
	select {
	case <-time.After(r.delay * time.Duration(i+1)):
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
		if !r.do(ctx, i) {
			break
		}
	}
	return nil, lastErr
}

func (r *retry) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	var lastErr error
	for i := 0; i < r.attempts; i++ {
		ch, err := r.c.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}
		lastErr = err
		if !r.do(ctx, i) {
			break
		}
	}
	return nil, lastErr
}

// UsageHook 在一次生成完成后收到用量与耗时,用于计量/计费/埋点(接 OTel、日志、账单等由你定)。
type UsageHook func(ctx context.Context, model string, u Usage, latency time.Duration)

// Metered 包一层 client,在 Generate/Stream 结束后回调 hook 上报用量与延迟。
// 流式场景在 channel 读完(Done/Err)后回调,累计最终 Usage。
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

func (m *metered) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	start := time.Now()
	src, err := m.c.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	if m.hook == nil {
		return src, nil
	}
	out := make(chan Chunk)
	go func() {
		defer close(out)
		var usage Usage
		for ch := range src {
			if ch.Usage != nil {
				usage = *ch.Usage
			}
			out <- ch
		}
		m.hook(ctx, req.Model, usage, time.Since(start))
	}()
	return out, nil
}
