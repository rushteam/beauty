package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	// ErrCircuitOpen 熔断器打开,请求被快速失败。
	ErrCircuitOpen = errCircuitOpen
	// ErrPoolSaturated 连接池已满且出现等待,提前拒绝以防雪崩。
	ErrPoolSaturated = errors.New("sqldb: connection pool saturated")
	// ErrConcurrencyLimited 并发舱壁已满。
	ErrConcurrencyLimited = errors.New("sqldb: concurrency limit exceeded")
)

// BreakerSettings 配置熔断器;零值字段用推荐默认。
type BreakerSettings struct {
	Threshold   float64       // 错误率阈值,默认 0.5
	Window      time.Duration // 统计窗口,默认 10s
	Cooldown    time.Duration // Open 冷却,默认 5s
	HalfOpenMax int           // 半开探测数,默认 3
	MinRequests int           // 最少样本,默认 10
}

func (s BreakerSettings) config() cbConfig {
	cfg := defaultCBConfig()
	if s.Threshold > 0 && s.Threshold <= 1 {
		cfg.threshold = s.Threshold
	}
	if s.Window > 0 {
		cfg.window = s.Window
	}
	if s.Cooldown > 0 {
		cfg.cooldown = s.Cooldown
	}
	if s.HalfOpenMax > 0 {
		cfg.halfOpenMax = s.HalfOpenMax
	}
	if s.MinRequests > 0 {
		cfg.minRequests = s.MinRequests
	}
	return cfg
}

// PathResilience 单条路径(读或写)的弹性配置。
type PathResilience struct {
	// Breaker 非 nil 时启用熔断;nil 表示关闭。
	Breaker *BreakerSettings

	// MaxConcurrent 限制同时在途 SQL 数(舱壁);0 表示不限。
	MaxConcurrent int

	// MaxConcurrentWait 等舱壁的最大时间;0 表示满则立即拒绝。
	MaxConcurrentWait time.Duration

	// Pool 可选:绑定底层 *sql.DB 做饱和检测(通常传 db.Primary())。
	Pool *sql.DB

	// IsFailure 自定义失败判定;nil 时用 DefaultIsFailure。
	IsFailure func(error) bool
}

// DefaultWriteResilience 写路径推荐默认(偏保守,适合防热点写拖垮池子)。
func DefaultWriteResilience(pool *sql.DB) PathResilience {
	return PathResilience{
		Breaker: &BreakerSettings{
			Threshold:   0.3,
			Window:      10 * time.Second,
			Cooldown:    5 * time.Second,
			HalfOpenMax: 2,
			MinRequests: 5,
		},
		Pool: pool,
	}
}

// DefaultReadResilience 读路径推荐默认(阈值更宽松,避免写故障误伤读)。
func DefaultReadResilience(pool *sql.DB) PathResilience {
	return PathResilience{
		Breaker: &BreakerSettings{
			Threshold:   0.6,
			Window:      10 * time.Second,
			Cooldown:    3 * time.Second,
			HalfOpenMax: 3,
			MinRequests: 10,
		},
		Pool: pool,
	}
}

// WithResilience 包装 DBTX,在 Exec/Query 前做池饱和检测、舱壁限流、熔断快速失败。
//
//	write := sqldb.WithResilience(db.Writer(), sqldb.DefaultWriteResilience(db.Primary()))
//	read  := sqldb.WithResilience(db.Reader(), sqldb.DefaultReadResilience(db.Primary()))
//
// 完整文档见 docs/db-resilience.md。
func WithResilience(inner DBTX, cfg PathResilience) DBTX {
	if inner == nil {
		return nil
	}
	return &resilientDBTX{inner: inner, guard: NewGuard(cfg)}
}

// ResilientWriter 便捷:Writer + 写路径弹性。
func (db *DB) ResilientWriter(cfg PathResilience) DBTX {
	if cfg.Pool == nil {
		cfg.Pool = db.primary
	}
	return WithResilience(db.Writer(), cfg)
}

// ResilientReader 便捷:Reader + 读路径弹性。
func (db *DB) ResilientReader(cfg PathResilience) DBTX {
	if cfg.Pool == nil {
		cfg.Pool = db.primary
	}
	return WithResilience(db.Reader(), cfg)
}

type resilientDBTX struct {
	inner DBTX
	guard *Guard
}

func (r *resilientDBTX) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	var res sql.Result
	err := r.guard.Call(ctx, func() error {
		var e error
		res, e = r.inner.ExecContext(ctx, q, args...)
		return e
	})
	return res, err
}

func (r *resilientDBTX) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	var stmt *sql.Stmt
	err := r.guard.Call(ctx, func() error {
		var e error
		stmt, e = r.inner.PrepareContext(ctx, q)
		return e
	})
	return stmt, err
}

func (r *resilientDBTX) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	var rows *sql.Rows
	err := r.guard.Call(ctx, func() error {
		var e error
		rows, e = r.inner.QueryContext(ctx, q, args...)
		return e
	})
	return rows, err
}

func (r *resilientDBTX) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if err := r.guard.AllowQueryRow(ctx); err != nil {
		return r.canceledRow(ctx, q, args, err)
	}
	return r.inner.QueryRowContext(ctx, q, args...)
}

func (r *resilientDBTX) canceledRow(ctx context.Context, q string, args []any, cause error) *sql.Row {
	cctx, cancel := context.WithCancelCause(ctx)
	cancel(cause)
	return r.inner.QueryRowContext(cctx, q, args...)
}

// InFlight 返回当前舱壁占用数(测试/观测用)。
func (r *resilientDBTX) InFlight() int64 {
	if r.guard == nil {
		return 0
	}
	return r.guard.InFlight()
}
