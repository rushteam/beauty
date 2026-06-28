// Package txn 提供跨域事务协调:让 wallet/storage/notification 等域包
// 在一个逻辑事务边界内原子提交或全部回滚。
//
// 背景:beauty 各域包(wallet/storage/account)各自管理状态,但应用常需
// "原子地:扣钱包 + 写存档 + 发通知"——任一步失败,前面已改的要回滚。
// beauty 是 DB-agnostic,因此做成通用的两阶段提交协调器(Two-Phase Commit):
//
//   - Prepare:各 Participant 校验能否提交,并把变更暂存到 staging(不落库);
//   - 全部 Prepare 成功 → 依次 Commit(落库);
//   - 任一 Prepare 失败 → 已 Prepare 的依次 Rollback(丢弃 staging)。
//
// 用法(业务层负责接线,各域包无需感知 txn):
//
//	walletStage := wallet.NewStaging()        // 域包提供 staging 视图(见下)
//	storageStage := storage.NewStaging()
//	coord := txn.New()
//	coord.Enlist("wallet", walletStage, walletStage.Commit, walletStage.Rollback)
//	coord.Enlist("storage", storageStage, storageStage.Commit, storageStage.Rollback)
//	err := coord.Run(func() error {
//	    // 在 staging 视图上操作,不直接改主库
//	    if _, _, err := walletStage.Apply(...); err != nil { return err }
//	    if err := storageStage.Set(...); err != nil { return err }
//	    return nil
//	})
//	// Run 返回 nil → 已 Commit;返回 err → 已 Rollback。
//
// 域包如何提供 staging:实现 Participant 接口即可。最简形式是"先在副本上操作,
// Commit 时把副本 swap 到主库,Rollback 时丢弃副本"。本包不规定 staging 实现,
// 只协调 Prepare/Commit/Rollback 的调用顺序与原子性。
//
// 并发安全:一个 Coordinator 一次只 Run 一个事务(内部加锁);多次 Run 串行。
// Prepare/Commit/Rollback 的错误会被收集,任一 Commit 失败后续 Commit 仍执行
// (best-effort),但会返回聚合错误——调用方据此做补偿。
package txn

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Participant 一个参与协调的域。三阶段:
//   - Prepare:校验 + 暂存变更。返回 error 表示无法提交(此时协调器会回滚)。
//   - Commit:把暂存变更落库。返回 error 表示提交失败(已尽力,调用方做补偿)。
//   - Rollback:丢弃暂存变更,恢复到 Prepare 前状态。
//
// 实现须幂等:Commit/Rollback 被重复调用不应有副作用。
type Participant interface {
	Prepare(ctx context.Context) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// ParticipantFunc 函数形式的 Participant(轻量场景:不需要结构体)。
// Prepare/Commit/Rollback 各自一个 func。
type ParticipantFunc struct {
	PrepareFn  func(ctx context.Context) error
	CommitFn   func(ctx context.Context) error
	RollbackFn func(ctx context.Context) error
}

func (p ParticipantFunc) Prepare(ctx context.Context) error {
	if p.PrepareFn == nil {
		return nil
	}
	return p.PrepareFn(ctx)
}
func (p ParticipantFunc) Commit(ctx context.Context) error {
	if p.CommitFn == nil {
		return nil
	}
	return p.CommitFn(ctx)
}
func (p ParticipantFunc) Rollback(ctx context.Context) error {
	if p.RollbackFn == nil {
		return nil
	}
	return p.RollbackFn(ctx)
}

// Coordinator 协调多个 Participant 的两阶段提交。
// 零值不可用,用 New 构造。
type Coordinator struct {
	mu      sync.Mutex
	runMu   sync.Mutex // 一次只 Run 一个事务
	ordered []namedParticipant
}

type namedParticipant struct {
	name string
	p    Participant
}

// New 创建 Coordinator。
func New() *Coordinator {
	return &Coordinator{}
}

// Enlist 注册一个 Participant,按注册顺序参与 Prepare/Commit(逆序 Rollback)。
// name 仅用于错误信息。可在 Run 前多次 Enlist。
func (c *Coordinator) Enlist(name string, p Participant) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ordered = append(c.ordered, namedParticipant{name: name, p: p})
}

// EnlistFunc 函数形式注册(等价于 Enlist(name, ParticipantFunc{...}))。
func (c *Coordinator) EnlistFunc(name string, prepare, commit, rollback func(ctx context.Context) error) {
	c.Enlist(name, ParticipantFunc{PrepareFn: prepare, CommitFn: commit, RollbackFn: rollback})
}

// Run 执行一个事务:body 在各 Participant 已 Prepare 后调用,body 内对
// staging 视图操作;body 返回 nil 则 Commit 全部,返回 err 则 Rollback 全部。
//
// 调用顺序:
//  1. 依次 Prepare(顺序);任一失败 → 已 Prepare 的逆序 Rollback,返回该错误;
//  2. body();body 返回 err → 已 Prepare 的逆序 Rollback,返回 body 的 err;
//  3. body 成功 → 依次 Commit(顺序);任一 Commit 失败 → 仍继续后续 Commit
//     (best-effort,避免部分提交导致不一致),最终返回聚合错误。
//
// 返回 nil 表示事务提交成功;返回 error 表示已 Rollback 或部分提交失败。
func (c *Coordinator) Run(ctx context.Context, body func() error) error {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.mu.Lock()
	participants := make([]namedParticipant, len(c.ordered))
	copy(participants, c.ordered)
	c.mu.Unlock()

	prepared := make([]namedParticipant, 0, len(participants))
	// 阶段 1:Prepare(顺序)。
	for _, np := range participants {
		if err := np.p.Prepare(ctx); err != nil {
			// 回滚已 Prepare 的(逆序)。
			rollbackAll(ctx, prepared)
			return fmt.Errorf("txn: prepare %s: %w", np.name, err)
		}
		prepared = append(prepared, np)
	}
	// 阶段 1.5:body(在 staging 上操作)。
	if err := body(); err != nil {
		rollbackAll(ctx, prepared)
		return fmt.Errorf("txn: body: %w", err)
	}
	// 阶段 2:Commit(顺序,best-effort)。
	var commitErrs []error
	for _, np := range prepared {
		if err := np.p.Commit(ctx); err != nil {
			commitErrs = append(commitErrs, fmt.Errorf("commit %s: %w", np.name, err))
		}
	}
	if len(commitErrs) > 0 {
		return fmt.Errorf("txn: %d commit(s) failed: %w", len(commitErrs), errors.Join(commitErrs...))
	}
	return nil
}

// rollbackAll 逆序回滚已 Prepare 的 Participant(best-effort,错误只记日志式收集)。
func rollbackAll(ctx context.Context, prepared []namedParticipant) {
	for i := len(prepared) - 1; i >= 0; i-- {
		_ = prepared[i].p.Rollback(ctx)
	}
}
