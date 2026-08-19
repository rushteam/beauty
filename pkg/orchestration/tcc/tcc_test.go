package tcc_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/orchestration/tcc"
)

// 模拟账户:支持冻结/确认/取消。
type account struct {
	balance int64
	frozen  int64
}

func (a *account) tryDeduct(amount int64) func(context.Context) error {
	return func(ctx context.Context) error {
		if a.balance-a.frozen < amount {
			return errors.New("insufficient balance")
		}
		a.frozen += amount
		return nil
	}
}

func (a *account) confirmDeduct(amount int64) func(context.Context) error {
	return func(ctx context.Context) error {
		a.balance -= amount
		a.frozen -= amount
		return nil
	}
}

func (a *account) cancelDeduct(amount int64) func(context.Context) error {
	return func(ctx context.Context) error {
		a.frozen -= amount
		return nil
	}
}

func TestTCC_AllConfirmed(t *testing.T) {
	buyer := &account{balance: 1000}
	seller := &account{balance: 500}

	tx := tcc.New("purchase").
		Branch(tcc.Branch{
			Name:    "buyer_deduct",
			Try:     buyer.tryDeduct(200),
			Confirm: buyer.confirmDeduct(200),
			Cancel:  buyer.cancelDeduct(200),
		}).
		Branch(tcc.Branch{
			Name: "seller_credit",
			Try: func(ctx context.Context) error {
				return nil // 预留:标记待入账
			},
			Confirm: func(ctx context.Context) error {
				seller.balance += 200
				return nil
			},
			Cancel: func(ctx context.Context) error {
				return nil
			},
		})

	res := tx.Execute(context.Background())
	if res.Failed() {
		t.Fatalf("should succeed: %v", res.Err)
	}
	if res.Status != tcc.StatusConfirmed {
		t.Fatalf("status = %v", res.Status)
	}
	if buyer.balance != 800 {
		t.Fatalf("buyer balance = %d, want 800", buyer.balance)
	}
	if buyer.frozen != 0 {
		t.Fatalf("buyer frozen = %d, want 0", buyer.frozen)
	}
	if seller.balance != 700 {
		t.Fatalf("seller balance = %d, want 700", seller.balance)
	}
}

func TestTCC_TryFails_CancelAll(t *testing.T) {
	buyer := &account{balance: 100}
	sellerTried := false

	tx := tcc.New("purchase").
		Branch(tcc.Branch{
			Name: "seller_reserve",
			Try: func(ctx context.Context) error {
				sellerTried = true
				return nil
			},
			Confirm: func(ctx context.Context) error { return nil },
			Cancel: func(ctx context.Context) error {
				sellerTried = false // Cancel 释放
				return nil
			},
		}).
		Branch(tcc.Branch{
			Name:    "buyer_deduct",
			Try:     buyer.tryDeduct(200), // 会失败
			Confirm: buyer.confirmDeduct(200),
			Cancel:  buyer.cancelDeduct(200),
		})

	res := tx.Execute(context.Background())
	if !res.Failed() {
		t.Fatal("should fail")
	}
	if res.Status != tcc.StatusCancelled {
		t.Fatalf("status = %v, want Cancelled", res.Status)
	}
	if res.FailedBranch != "buyer_deduct" {
		t.Fatalf("failed branch = %s", res.FailedBranch)
	}
	// seller 的 Cancel 应已被调用
	if sellerTried {
		t.Fatal("seller should be cancelled")
	}
	// buyer 未冻结(Try 失败,不 Cancel 当前分支)
	if buyer.frozen != 0 {
		t.Fatalf("buyer frozen = %d", buyer.frozen)
	}
}

func TestTCC_ConfirmFails(t *testing.T) {
	a := &account{balance: 1000}
	confirmCalls := 0

	tx := tcc.New("broken_confirm").
		Branch(tcc.Branch{
			Name:    "deduct",
			Try:     a.tryDeduct(100),
			Confirm: a.confirmDeduct(100),
			Cancel:  a.cancelDeduct(100),
		}).
		Branch(tcc.Branch{
			Name: "broken",
			Try:  func(ctx context.Context) error { return nil },
			Confirm: func(ctx context.Context) error {
				confirmCalls++
				return errors.New("confirm crash")
			},
			Cancel: func(ctx context.Context) error { return nil },
		})

	res := tx.Execute(context.Background())
	if res.Status != tcc.StatusConfirmFailed {
		t.Fatalf("status = %v, want ConfirmFailed", res.Status)
	}
	// 第一个分支的 Confirm 仍应被调用(best-effort)
	if a.balance != 900 {
		t.Fatalf("balance = %d, want 900 (first confirm should succeed)", a.balance)
	}
}

func TestTCC_CancelFails(t *testing.T) {
	tx := tcc.New("cancel_fail").
		Branch(tcc.Branch{
			Name: "step1",
			Try:  func(ctx context.Context) error { return nil },
			Cancel: func(ctx context.Context) error {
				return errors.New("cancel broken")
			},
		}).
		Branch(tcc.Branch{
			Name: "step2",
			Try:  func(ctx context.Context) error { return errors.New("fail") },
		})

	res := tx.Execute(context.Background())
	if res.Status != tcc.StatusCancelFailed {
		t.Fatalf("status = %v, want CancelFailed", res.Status)
	}
}

func TestTCC_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	tx := tcc.New("ctx_cancel").
		Branch(tcc.Branch{
			Name: "step1",
			Try:  func(ctx context.Context) error { return nil },
		})

	res := tx.Execute(ctx)
	if res.Status == tcc.StatusConfirmed {
		t.Fatal("cancelled context should prevent execution")
	}
}

func TestTCC_NilConfirmCancel(t *testing.T) {
	tx := tcc.New("nil_handlers").
		Branch(tcc.Branch{
			Name: "simple",
			Try:  func(ctx context.Context) error { return nil },
			// Confirm 和 Cancel 都是 nil
		})

	res := tx.Execute(context.Background())
	if res.Failed() {
		t.Fatalf("nil handlers should not fail: %v", res.Err)
	}
}

func TestTCC_WithConfirmRetry(t *testing.T) {
	var attempts atomic.Int32

	tx := tcc.New("retry_confirm",
		tcc.WithConfirmRetry(2, 0), // 重试 2 次
	).
		Branch(tcc.Branch{
			Name: "flaky",
			Try:  func(ctx context.Context) error { return nil },
			Confirm: func(ctx context.Context) error {
				n := attempts.Add(1)
				if n < 3 {
					return errors.New("transient error")
				}
				return nil
			},
			Cancel: func(ctx context.Context) error { return nil },
		})

	res := tx.Execute(context.Background())
	if res.Failed() {
		t.Fatalf("should succeed after retries: %v", res.Err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestTCC_WithOnConfirmHook(t *testing.T) {
	var hookCalls int

	tx := tcc.New("hooks",
		tcc.WithOnConfirm(func(branch string, attempt int, err error) {
			hookCalls++
		}),
	).
		Branch(tcc.Branch{
			Name:    "step",
			Try:     func(ctx context.Context) error { return nil },
			Confirm: func(ctx context.Context) error { return nil },
		})

	tx.Execute(context.Background())
	if hookCalls != 1 {
		t.Fatalf("hook calls = %d", hookCalls)
	}
}

func TestStatus_String(t *testing.T) {
	cases := []struct {
		s    tcc.Status
		want string
	}{
		{tcc.StatusConfirmed, "confirmed"},
		{tcc.StatusCancelled, "cancelled"},
		{tcc.StatusCancelFailed, "cancel_failed"},
		{tcc.StatusConfirmFailed, "confirm_failed"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Fatalf("%d.String() = %s, want %s", c.s, got, c.want)
		}
	}
}

// 端到端:模拟"购买道具"完整流程
func TestTCC_E2E_ItemPurchase(t *testing.T) {
	// 买家: 1000 金币
	buyerGold := &account{balance: 1000}
	// 商店: 10 件库存
	inventory := int64(10)
	var inventoryFrozen int64
	// 买家背包
	var itemReceived bool

	tx := tcc.New("buy_item").
		Branch(tcc.Branch{
			Name:    "deduct_gold",
			Try:     buyerGold.tryDeduct(300),
			Confirm: buyerGold.confirmDeduct(300),
			Cancel:  buyerGold.cancelDeduct(300),
		}).
		Branch(tcc.Branch{
			Name: "reserve_inventory",
			Try: func(ctx context.Context) error {
				if inventory-inventoryFrozen <= 0 {
					return errors.New("out of stock")
				}
				inventoryFrozen++
				return nil
			},
			Confirm: func(ctx context.Context) error {
				inventory--
				inventoryFrozen--
				return nil
			},
			Cancel: func(ctx context.Context) error {
				inventoryFrozen--
				return nil
			},
		}).
		Branch(tcc.Branch{
			Name: "add_to_backpack",
			Try:  func(ctx context.Context) error { return nil },
			Confirm: func(ctx context.Context) error {
				itemReceived = true
				return nil
			},
			Cancel: func(ctx context.Context) error { return nil },
		})

	res := tx.Execute(context.Background())
	if res.Failed() {
		t.Fatalf("purchase failed: %v", res.Err)
	}
	if buyerGold.balance != 700 {
		t.Fatalf("gold = %d, want 700", buyerGold.balance)
	}
	if inventory != 9 {
		t.Fatalf("inventory = %d, want 9", inventory)
	}
	if !itemReceived {
		t.Fatal("item not received")
	}
}
