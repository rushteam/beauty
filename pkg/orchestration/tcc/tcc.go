// Package tcc 提供 TCC(Try-Confirm-Cancel)事务编排原语,用于涉及资金/道具的
// 强一致性分布式操作。
//
// 与 saga / txn 的区别(三者互补):
//
//   - txn(2PC):同进程内 Prepare→Commit/Rollback,参与者能"预留不提交",适合内存态域包;
//   - saga:跨服务正向执行→补偿,Action 当场生效(已落库),失败靠"反向操作"抵消;
//   - tcc:跨服务预留→确认/取消,Try 只"冻结资源"(预留额度/锁定库存)不真正生效,
//     全部 Try 成功后统一 Confirm;任一 Try 失败则对已 Try 的统一 Cancel。
//     资金场景的标准方案:先冻结余额→全部冻结成功→扣款;比 saga 多了"冻结"这层保护。
//
// 执行语义:
//
//	Phase 1 (Try):顺序执行每个 Branch 的 Try(冻结/预留);
//	  → 任一 Try 失败:已 Try 的逆序 Cancel(解冻/释放),返回错误;
//	  → 全部 Try 成功:进入 Phase 2。
//
//	Phase 2 (Confirm):顺序执行每个 Branch 的 Confirm(真正扣款/转移);
//	  → 任一 Confirm 失败:收集错误,继续后续 Confirm(best-effort);
//	  → 全部成功:StatusConfirmed。
//
// 关键约束:
//   - Try 必须幂等且可安全 Cancel:Cancel 释放 Try 冻结的资源;
//   - Confirm 必须幂等:Confirm 可能因重试重复调用;
//   - Cancel 必须幂等:Cancel 是最终一致性的兜底;
//   - 本包不持久化(纯内存编排):进程崩溃则进行中的 TCC 丢失,须靠幂等+重投恢复。
//
// 并发安全:一个 Tx 的 Execute 非并发安全;不同 Tx 实例相互独立。
// 零值不可用:用 New 构造。
package tcc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/foundation/safe"
)

// Branch 是 TCC 的一个分支(一个参与服务的操作)。
type Branch struct {
	// Name 分支名,用于结果与日志。
	Name string
	// Try 预留/冻结资源。返回 error 表示预留失败,触发已预留分支的 Cancel。
	Try func(ctx context.Context) error
	// Confirm 确认提交(真正扣款/转移)。在所有分支 Try 成功后调用。须幂等。
	Confirm func(ctx context.Context) error
	// Cancel 取消预留(解冻/释放)。在 Try 失败时对已预留分支调用。须幂等。
	Cancel func(ctx context.Context) error
}

// Status 是 TCC 执行的终态。
type Status int

const (
	// StatusConfirmed 全部 Try + Confirm 成功。
	StatusConfirmed Status = iota
	// StatusCancelled 某分支 Try 失败,已成功 Cancel 所有已预留分支(资源已释放)。
	StatusCancelled
	// StatusCancelFailed 某分支 Try 失败,且 Cancel 过程中有分支 Cancel 失败(须人工介入)。
	StatusCancelFailed
	// StatusConfirmFailed 全部 Try 成功,但某分支 Confirm 失败(须人工介入/重试 Confirm)。
	StatusConfirmFailed
)

func (s Status) String() string {
	switch s {
	case StatusConfirmed:
		return "confirmed"
	case StatusCancelled:
		return "cancelled"
	case StatusCancelFailed:
		return "cancel_failed"
	case StatusConfirmFailed:
		return "confirm_failed"
	default:
		return "unknown"
	}
}

// BranchResult 单个分支的执行明细。
type BranchResult struct {
	Name       string        // 分支名
	TryErr     error         // Try 阶段错误(nil=成功)
	ConfirmErr error         // Confirm 阶段错误
	CancelErr  error         // Cancel 阶段错误
	Cancelled  bool          // 是否执行了 Cancel
	Confirmed  bool          // 是否执行了 Confirm
	TryDur     time.Duration // Try 耗时
}

// Result 是 Execute 的完整结果。
type Result struct {
	Status       Status
	Err          error          // 触发取消的原始 Try 失败(StatusConfirmed 时为 nil)
	FailedBranch string         // 失败分支名(StatusConfirmed 时为空)
	Branches     []BranchResult // 各分支执行明细(按注册顺序)
}

// Failed 返回事务是否未成功确认。
func (r *Result) Failed() bool { return r.Status != StatusConfirmed }

// config 配置。
type config struct {
	confirmRetries int
	cancelRetries  int
	confirmDelay   time.Duration
	cancelDelay    time.Duration
	maxRetryDelay  time.Duration
	onCancel       func(branch string, attempt int, err error)
	onConfirm      func(branch string, attempt int, err error)
}

// Option 配置 Tx。
type Option func(*config)

// WithConfirmRetry 设置 Confirm 失败重试次数与退避间隔。
func WithConfirmRetry(retries int, delay time.Duration) Option {
	return func(c *config) {
		if retries >= 0 {
			c.confirmRetries = retries
		}
		if delay > 0 {
			c.confirmDelay = delay
		}
	}
}

// WithCancelRetry 设置 Cancel 失败重试次数与退避间隔。
func WithCancelRetry(retries int, delays ...time.Duration) Option {
	return func(c *config) {
		if retries >= 0 {
			c.cancelRetries = retries
		}
		if len(delays) > 0 && delays[0] > 0 {
			c.cancelDelay = delays[0]
		}
	}
}

// WithMaxRetryDelay 设置指数退避的最大延迟上限,防止退避时间溢出。
func WithMaxRetryDelay(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.maxRetryDelay = d
		}
	}
}

// WithOnCancel 设置 Cancel 回调(每次 Cancel 尝试后触发)。
func WithOnCancel(fn func(branch string, attempt int, err error)) Option {
	return func(c *config) { c.onCancel = fn }
}

// WithOnConfirm 设置 Confirm 回调(每次 Confirm 尝试后触发)。
func WithOnConfirm(fn func(branch string, attempt int, err error)) Option {
	return func(c *config) { c.onConfirm = fn }
}

// Tx 一个 TCC 事务。零值不可用,用 New 构造。
type Tx struct {
	name     string
	cfg      config
	branches []Branch
}

// New 创建 TCC 事务。name 用于结果与日志。
func New(name string, opts ...Option) *Tx {
	cfg := config{
		confirmDelay:  100 * time.Millisecond,
		cancelDelay:   100 * time.Millisecond,
		maxRetryDelay: 10 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Tx{name: name, cfg: cfg}
}

// Branch 追加一个分支(链式)。
func (t *Tx) Branch(b Branch) *Tx {
	t.branches = append(t.branches, b)
	return t
}

// Execute 执行 TCC:Phase 1 (Try) → Phase 2 (Confirm or Cancel)。
func (t *Tx) Execute(ctx context.Context) *Result {
	res := &Result{Status: StatusConfirmed}
	res.Branches = make([]BranchResult, len(t.branches))

	// Phase 1: Try
	tried := make([]int, 0, len(t.branches))
	for i, b := range t.branches {
		if err := ctx.Err(); err != nil {
			res.Err = fmt.Errorf("tcc %q: context done before try %q: %w", t.name, b.Name, err)
			res.FailedBranch = b.Name
			res.Branches[i] = BranchResult{Name: b.Name, TryErr: res.Err}
			t.cancelAll(ctx, tried, res)
			return res
		}

		start := time.Now()
		err := safe.Run(func() error { return b.Try(ctx) })
		res.Branches[i] = BranchResult{Name: b.Name, TryErr: err, TryDur: time.Since(start)}

		if err != nil {
			res.Err = fmt.Errorf("tcc %q: try %q failed: %w", t.name, b.Name, err)
			res.FailedBranch = b.Name
			t.cancelAll(ctx, tried, res)
			return res
		}
		tried = append(tried, i)
	}

	// Phase 2: Confirm
	t.confirmAll(ctx, tried, res)
	return res
}

// confirmAll 顺序确认所有已 Try 的分支(best-effort:一个失败不影响后续)。
// 使用 context.WithoutCancel 保证父 ctx 取消不会中断 Confirm 流程。
func (t *Tx) confirmAll(ctx context.Context, tried []int, res *Result) {
	confirmCtx := context.WithoutCancel(ctx)
	var confirmErrs []error
	for _, idx := range tried {
		b := t.branches[idx]
		if b.Confirm == nil {
			res.Branches[idx].Confirmed = true
			continue
		}

		err := t.retryOp(confirmCtx, b.Name, b.Confirm, t.cfg.confirmRetries, t.cfg.confirmDelay, t.cfg.onConfirm)
		res.Branches[idx].Confirmed = true
		res.Branches[idx].ConfirmErr = err
		if err != nil {
			confirmErrs = append(confirmErrs, fmt.Errorf("confirm %s: %w", b.Name, err))
		}
	}
	if len(confirmErrs) > 0 {
		res.Status = StatusConfirmFailed
		res.Err = errors.Join(confirmErrs...)
	}
}

// cancelAll 逆序取消已 Try 的分支。
func (t *Tx) cancelAll(ctx context.Context, tried []int, res *Result) {
	compCtx := context.WithoutCancel(ctx)
	res.Status = StatusCancelled
	for i := len(tried) - 1; i >= 0; i-- {
		idx := tried[i]
		b := t.branches[idx]
		if b.Cancel == nil {
			res.Branches[idx].Cancelled = true
			continue
		}

		err := t.retryOp(compCtx, b.Name, b.Cancel, t.cfg.cancelRetries, t.cfg.cancelDelay, t.cfg.onCancel)
		res.Branches[idx].Cancelled = true
		res.Branches[idx].CancelErr = err
		if err != nil {
			res.Status = StatusCancelFailed
		}
	}
}

func (t *Tx) retryOp(ctx context.Context, name string, op func(context.Context) error, retries int, baseDelay time.Duration, hook func(string, int, error)) error {
	attempts := retries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		lastErr = safe.Run(func() error { return op(ctx) })
		if hook != nil {
			hook(name, attempt, lastErr)
		}
		if lastErr == nil {
			return nil
		}
		if attempt < attempts {
			shift := min(attempt-1, 30)
			delay := baseDelay * time.Duration(1<<shift)
			if t.cfg.maxRetryDelay > 0 && delay > t.cfg.maxRetryDelay {
				delay = t.cfg.maxRetryDelay
			}
			time.Sleep(delay)
		}
	}
	return lastErr
}
