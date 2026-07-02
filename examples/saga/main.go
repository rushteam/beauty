// saga 示例:跨服务 Saga 编排(抽卡流程)。
//
// 演示 pkg/saga:顺序正向操作 + 失败逆序补偿。流程 "扣钻石 → 发卡 → 写记录",
// 中途发卡失败,自动逆序补偿(退钻石)。正向/补偿都用 wallet.ApplyTx 的幂等键,
// 演示 saga + idempotency + wallet 三件套如何咬合(补偿重试不会退两次款)。
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/rushteam/beauty/pkg/domain/wallet"
	"github.com/rushteam/beauty/pkg/saga"
)

func main() {
	w := wallet.New()
	w.Apply("player-1", wallet.WalletMap{"gem": 500}, "init", 1)
	fmt.Printf("初始钻石: %d\n\n", w.Balance("player-1")["gem"])

	// 场景:发卡服务故障,saga 应扣钻石后补偿退回。
	grantServiceDown := true

	s := saga.New("draw-card", saga.WithCompensationRetry(2, 0)).
		Step("deduct-gem",
			// 正向:扣 100 钻石,幂等键 tx-draw-1。
			func(ctx context.Context) error {
				_, _, replayed, err := w.ApplyTx("tx-draw-1", "player-1", wallet.WalletMap{"gem": -100}, "draw", 2)
				fmt.Printf("  [扣钻石] replayed=%v err=%v\n", replayed, err)
				return err
			},
			// 补偿:退 100 钻石,不同的幂等键 tx-refund-1(重试也只退一次)。
			func(ctx context.Context) error {
				_, _, replayed, err := w.ApplyTx("tx-refund-1", "player-1", wallet.WalletMap{"gem": 100}, "refund", 3)
				fmt.Printf("  [退钻石] replayed=%v err=%v\n", replayed, err)
				return err
			}).
		Step("grant-card",
			func(ctx context.Context) error {
				if grantServiceDown {
					return errors.New("发奖服务不可用")
				}
				return nil
			}, nil)

	fmt.Println("执行 saga:")
	res := s.Execute(context.Background())

	fmt.Printf("\n终态: %s\n", res.Status)
	fmt.Printf("失败步骤: %s(%v)\n", res.FailedStep, res.Err)
	fmt.Printf("最终钻石: %d(扣了又退回,一致)\n", w.Balance("player-1")["gem"])

	fmt.Println("\n步骤明细:")
	for _, sr := range res.Steps {
		fmt.Printf("  - %-12s action_err=%v compensated=%v\n", sr.Name, sr.ActionErr, sr.Compensated)
	}
}
