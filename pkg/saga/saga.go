// Package saga 提供跨服务 Saga 编排:按顺序执行一组"正向操作",任一步失败时
// 逆序执行已成功步骤的"补偿操作",达成最终一致。
//
// 与 pkg/txn 的区别(互补):
//   - txn 是同进程 2PC:参与者在内存里 Prepare(暂存不提交),失败真回滚;
//     要求所有参与者能被同步调用且支持"预留不提交"。
//   - saga 是跨服务最终一致:每步正向操作当场生效(如 RPC 落库,无法预留),
//     失败时靠"补偿"(业务定义的反向操作,是一笔新操作而非回滚)把已做的抵消掉。
//     用于参与者分散在多个服务、无法 2PC 的场景。
//
// 执行语义(Execute):
//   - 按注册顺序执行每个 Step.Action;
//   - 全部成功 → StatusCommitted;
//   - 第 k 步失败 → 逆序补偿第 k-1…1 步(跳过无补偿的步骤):
//     补偿全成功 → StatusCompensated(数据一致,返回原始失败原因);
//     某步补偿失败 → StatusCompensationFailed(数据不一致,须告警 + 人工介入)。
//
// 关键约束(MVP,纯内存):
//   - 补偿必须幂等:补偿会因重试重复执行。责任在调用方——推荐正/反向都用
//     幂等键实现(如 wallet.ApplyTx(txID) / wallet.ApplyTx(refundID));
//   - 不持久化:协调器进程崩溃则进行中的 saga 丢失、无法恢复。仅适合"单进程内
//     编排多个 RPC、进程存活期间完成"的流程,不是可靠分布式事务;
//   - 无隔离性:执行中的中间态(钱扣了货没发)对其他请求可见,业务需容忍;
//   - 当步 Action 失败不补偿当步——只补偿已成功的前序步骤,当步的部分副作用
//     须靠 Action 自身幂等 / 其补偿设计覆盖。
//
// Action/Compensate 中的 panic 被 pkg/safe 转为 error(走正常失败/补偿流程)。
// 补偿阶段使用 context.WithoutCancel:原请求 ctx 取消不应中断补偿(副作用须补完)。
//
// 崩溃恢复:本包不持久化。生产环境依赖「可重投的触发源」(如 MQ at-least-once)
// 实现恢复——协调器崩溃后消息重投、saga 从头重跑,靠「幂等的 Action/Compensate」
// 保证不产生重复副作用。
// 前提:幂等键必须来自触发消息、跨重投稳定(如 msg.OrderID),不可用 idgen/uuid
// 现场生成——现场生成会让每次重投的键都不同,幂等失效、重复扣款。
// 仅当触发源不可重投(如不会重试的同步请求)时,才需要外部持久化 saga 状态并在
// 重启时恢复进度——那种场景本包不覆盖。
//
// 零值不可用,用 New 构造。单个 Saga 的 Execute 非并发安全(一次编排一个流程);
// 不同 Saga 实例相互独立。
package saga

import (
	"context"
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
	"github.com/rushteam/beauty/pkg/safe"
)

// Step 一个 Saga 步骤:正向操作 + 补偿操作。
type Step struct {
	// Name 步骤名,用于结果与日志。
	Name string
	// Action 正向操作。返回 error 表示本步失败,触发对已成功前序步骤的补偿。
	Action func(ctx context.Context) error
	// Compensate 补偿操作(Action 的业务反向)。nil 表示本步无需补偿(如只读步骤)。
	// 须幂等:可能因重试被重复调用。
	Compensate func(ctx context.Context) error
}

// Status 是 Saga 执行的终态。
type Status int

const (
	// StatusCommitted 所有正向步骤成功。
	StatusCommitted Status = iota
	// StatusCompensated 某步失败,但已成功补偿所有前序步骤(数据一致)。
	StatusCompensated
	// StatusCompensationFailed 某步失败,且补偿过程中又有步骤补偿失败(数据不一致,须人工介入)。
	StatusCompensationFailed
)

func (s Status) String() string {
	switch s {
	case StatusCommitted:
		return "committed"
	case StatusCompensated:
		return "compensated"
	case StatusCompensationFailed:
		return "compensation_failed"
	default:
		return "unknown"
	}
}

// StepResult 单个步骤的执行明细。
type StepResult struct {
	Name          string        // 步骤名
	ActionErr     error         // 正向操作错误(nil=成功)
	Compensated   bool          // 是否执行了补偿
	CompensateErr error         // 补偿错误(仅 Compensated=true 时有意义)
	CompensateTry int           // 补偿实际尝试次数
	Duration      time.Duration // 正向操作耗时
}

// Result 是 Execute 的完整结果。
type Result struct {
	// Name Saga 名。
	Name string
	// Status 终态。
	Status Status
	// Err 触发补偿的原始失败(StatusCommitted 时为 nil)。
	Err error
	// FailedStep 失败步骤名(StatusCommitted 时为空)。
	FailedStep string
	// Steps 各步骤执行明细(按注册顺序)。
	Steps []StepResult
}

// Failed 返回 Saga 是否未成功提交(需要调用方处理)。
func (r *Result) Failed() bool { return r.Status != StatusCommitted }

// config 配置。
type config struct {
	compRetries    int
	compRetryDelay time.Duration
	onCompensate   func(step string, attempt int, err error)
}

// Option 配置 Saga。
type Option func(*config)

// WithCompensationRetry 设置补偿失败的重试次数与基础退避间隔(指数退避)。
// retries 为额外重试次数(0=只试一次),delay 为首次退避(第 n 次退避 = delay<<n)。
// 补偿是挽救不一致的最后手段,适度重试能显著降低 StatusCompensationFailed 概率。
func WithCompensationRetry(retries int, delay time.Duration) Option {
	return func(c *config) {
		c.compRetries = retries
		c.compRetryDelay = delay
	}
}

// WithOnCompensate 设置补偿回调(每次补偿尝试后触发,用于日志 / metric / 审计)。
// attempt 从 1 计数,err==nil 表示该次补偿成功。
func WithOnCompensate(fn func(step string, attempt int, err error)) Option {
	return func(c *config) { c.onCompensate = fn }
}

// Saga 一个 Saga 编排:一组有序步骤 + 补偿策略。零值不可用,用 New 构造。
type Saga struct {
	name  string
	cfg   config
	steps []Step
}

// New 创建 Saga。name 用于结果与日志。
func New(name string, opts ...Option) *Saga {
	cfg := config{compRetries: 0, compRetryDelay: 100 * time.Millisecond}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.compRetries < 0 {
		cfg.compRetries = 0
	}
	return &Saga{name: name, cfg: cfg}
}

// Step 追加一个步骤(链式)。compensate 可为 nil(本步无需补偿)。
func (s *Saga) Step(name string, action, compensate func(ctx context.Context) error) *Saga {
	s.steps = append(s.steps, Step{Name: name, Action: action, Compensate: compensate})
	return s
}

// AddStep 追加一个已构造的 Step(链式)。
func (s *Saga) AddStep(step Step) *Saga {
	s.steps = append(s.steps, step)
	return s
}

// Execute 顺序执行所有步骤的正向操作;任一步失败则逆序补偿已成功的步骤。
// 返回带完整明细的 Result(不返回 error——失败语义在 Result.Status/Err 中)。
//
// ctx 取消/超时会被正向阶段感知(在下一步开始前检查),已开始的 Action 由其自身
// 响应 ctx;随后对已成功步骤执行补偿。补偿阶段用 context.WithoutCancel 派生,
// 不受原 ctx 取消影响。
func (s *Saga) Execute(ctx context.Context) *Result {
	res := &Result{Name: s.name, Status: StatusCommitted}

	completed := make([]int, 0, len(s.steps)) // 已成功步骤的下标(供逆序补偿)
	for i, step := range s.steps {
		// 进入下一步前检查 ctx,已取消则失败并补偿。
		if err := ctx.Err(); err != nil {
			res.Err = fmt.Errorf("saga %q: context done before step %q: %w", s.name, step.Name, err)
			res.FailedStep = step.Name
			res.Steps = append(res.Steps, StepResult{Name: step.Name, ActionErr: res.Err})
			s.compensate(ctx, completed, res)
			return res
		}

		start := time.Now()
		err := safe.Run(func() error { return step.Action(ctx) })
		sr := StepResult{Name: step.Name, ActionErr: err, Duration: time.Since(start)}
		res.Steps = append(res.Steps, sr)

		if err != nil {
			res.Err = fmt.Errorf("saga %q: step %q action failed: %w", s.name, step.Name, err)
			res.FailedStep = step.Name
			s.compensate(ctx, completed, res)
			return res
		}
		completed = append(completed, i)
	}
	return res
}

// compensate 逆序补偿 completed 中的步骤,把结果写回 res.Steps 对应项。
// 任一步补偿最终失败 → res.Status = StatusCompensationFailed;否则 StatusCompensated。
func (s *Saga) compensate(ctx context.Context, completed []int, res *Result) {
	// 补偿不受原 ctx 取消影响:副作用须补完。
	compCtx := context.WithoutCancel(ctx)
	res.Status = StatusCompensated

	// 补偿重试的退避序列复用 pkg/backoff:base=compRetryDelay、factor=2、无抖动,
	// 与历史行为(delay<<(attempt-1))一致。
	policy := backoff.New(
		backoff.WithBase(s.cfg.compRetryDelay),
		backoff.WithFactor(2),
		backoff.WithJitter(backoff.JitterNone),
		backoff.WithMax(0),
	)

	for i := len(completed) - 1; i >= 0; i-- {
		idx := completed[i]
		step := s.steps[idx]
		if step.Compensate == nil {
			continue // 无补偿的步骤跳过
		}

		var lastErr error
		attempts := s.cfg.compRetries + 1
		var tried int
		for attempt := 1; attempt <= attempts; attempt++ {
			tried = attempt
			lastErr = safe.Run(func() error { return step.Compensate(compCtx) })
			if s.cfg.onCompensate != nil {
				s.cfg.onCompensate(step.Name, attempt, lastErr)
			}
			if lastErr == nil {
				break
			}
			if attempt < attempts {
				// 指数退避后重试(attempt 从 1 计,Duration(attempt-1) 对应 base<<(attempt-1))。
				time.Sleep(policy.Duration(attempt - 1))
			}
		}

		res.Steps[idx].Compensated = true
		res.Steps[idx].CompensateErr = lastErr
		res.Steps[idx].CompensateTry = tried
		if lastErr != nil {
			res.Status = StatusCompensationFailed
		}
	}
}
