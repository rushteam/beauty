package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// Guard 封装熔断 + 舱壁 + 连接池饱和检测,供 DBTX 包装与 ORM Hook 复用。
type Guard struct {
	breaker   *circuitBreaker
	pool      *sql.DB
	isFailure func(error) bool

	semCap  int64
	semWait time.Duration
	semUsed int64
	semMu   sync.Mutex
	semCond *sync.Cond

	poolMu          sync.Mutex
	lastWaitCount   int64
	lastWaitChecked time.Time
}

// NewGuard 按 PathResilience 构造守卫。
func NewGuard(cfg PathResilience) *Guard {
	g := &Guard{
		pool:      cfg.Pool,
		isFailure: cfg.IsFailure,
	}
	if g.isFailure == nil {
		g.isFailure = DefaultIsFailure
	}
	if cfg.Breaker != nil {
		g.breaker = newCircuitBreaker(cfg.Breaker.config())
	}
	if cfg.MaxConcurrent > 0 {
		g.semCap = int64(cfg.MaxConcurrent)
		g.semWait = cfg.MaxConcurrentWait
	}
	return g
}

// Call 执行一次受保护的 DB 操作(Exec/Query 等)。
func (g *Guard) Call(ctx context.Context, fn func() error) error {
	if err := g.checkPool(); err != nil {
		return err
	}
	run := func() error {
		if err := g.acquire(ctx); err != nil {
			return err
		}
		defer g.release()
		return fn()
	}
	if g.breaker == nil {
		return run()
	}
	var opErr error
	err := g.breaker.Do(func() error {
		opErr = run()
		if g.isFailure(opErr) {
			return opErr
		}
		return nil
	})
	if errors.Is(err, errCircuitOpen) {
		return ErrCircuitOpen
	}
	return opErr
}

// Admit ORM Hook 的 Before 阶段:池检查 + 熔断状态 + 舱壁占用。
func (g *Guard) Admit(ctx context.Context) error {
	if err := g.checkPool(); err != nil {
		return err
	}
	if g.breaker != nil {
		if err := g.breaker.allow(); err != nil {
			return ErrCircuitOpen
		}
	}
	return g.acquire(ctx)
}

// Finish ORM Hook 的 After 阶段:释放舱壁并更新熔断统计。
func (g *Guard) Finish(err error) {
	g.release()
	if g.breaker == nil {
		return
	}
	_ = g.breaker.Do(func() error {
		if g.isFailure(err) {
			return err
		}
		return nil
	})
}

// AllowQueryRow QueryRow 路径:仅池 + 熔断状态检查(不占用舱壁)。
func (g *Guard) AllowQueryRow(ctx context.Context) error {
	if err := g.checkPool(); err != nil {
		return err
	}
	if g.breaker != nil {
		if err := g.breaker.allow(); err != nil {
			return ErrCircuitOpen
		}
	}
	return nil
}

// InFlight 当前舱壁占用数。
func (g *Guard) InFlight() int64 {
	g.semMu.Lock()
	defer g.semMu.Unlock()
	return g.semUsed
}

func (g *Guard) checkPool() error {
	if g.pool == nil {
		return nil
	}
	stats := g.pool.Stats()
	if stats.MaxOpenConnections <= 0 || stats.InUse < stats.MaxOpenConnections {
		return nil
	}
	g.poolMu.Lock()
	defer g.poolMu.Unlock()
	now := time.Now()
	if stats.WaitCount > g.lastWaitCount && now.Sub(g.lastWaitChecked) < 2*time.Second {
		return ErrPoolSaturated
	}
	g.lastWaitCount = stats.WaitCount
	g.lastWaitChecked = now
	return nil
}

func (g *Guard) initCond() {
	g.semMu.Lock()
	defer g.semMu.Unlock()
	if g.semCond == nil && g.semCap > 0 {
		g.semCond = sync.NewCond(&g.semMu)
	}
}

func (g *Guard) acquire(ctx context.Context) error {
	if g.semCap <= 0 {
		return nil
	}
	g.initCond()

	g.semMu.Lock()
	if g.semUsed < g.semCap {
		g.semUsed++
		g.semMu.Unlock()
		return nil
	}
	if g.semWait == 0 {
		g.semMu.Unlock()
		return ErrConcurrencyLimited
	}
	deadline := time.Now().Add(g.semWait)
	for g.semUsed >= g.semCap {
		g.semMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
		if time.Now().After(deadline) {
			return ErrConcurrencyLimited
		}
		g.semMu.Lock()
	}
	g.semUsed++
	g.semMu.Unlock()
	return nil
}

func (g *Guard) release() {
	if g.semCap <= 0 {
		return
	}
	g.semMu.Lock()
	g.semUsed--
	if g.semUsed < 0 {
		g.semUsed = 0
	}
	cond := g.semCond
	g.semMu.Unlock()
	if cond != nil {
		cond.Broadcast()
	}
}
