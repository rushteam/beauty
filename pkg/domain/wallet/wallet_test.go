package wallet_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/domain/wallet"
)

func TestWallet_ApplyBasic(t *testing.T) {
	w := wallet.New()
	bal, l, err := w.Apply("u1", wallet.WalletMap{"gold": 100}, "init", 1)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if bal["gold"] != 100 {
		t.Fatalf("got %d, want 100", bal["gold"])
	}
	if l.ID != 1 || l.OwnerID != "u1" || l.Changeset["gold"] != 100 {
		t.Fatalf("ledger: %+v", l)
	}
	if w.Balance("u1")["gold"] != 100 {
		t.Fatalf("balance not 100")
	}
}

func TestWallet_ApplyMultiple(t *testing.T) {
	w := wallet.New()
	w.Apply("u1", wallet.WalletMap{"gold": 100}, "init", 1)
	w.Apply("u1", wallet.WalletMap{"gold": -30}, "buy", 2)
	w.Apply("u1", wallet.WalletMap{"gem": 5, "gold": 10}, "reward", 3)
	bal := w.Balance("u1")
	if bal["gold"] != 80 || bal["gem"] != 5 {
		t.Fatalf("bal=%v", bal)
	}
	ledgers := w.Ledgers("u1")
	if len(ledgers) != 3 {
		t.Fatalf("ledger count=%d", len(ledgers))
	}
	if ledgers[2].ID != 3 {
		t.Fatalf("last id=%d", ledgers[2].ID)
	}
}

func TestWallet_Overdraft(t *testing.T) {
	w := wallet.New()
	w.Apply("u1", wallet.WalletMap{"gold": 50}, "init", 1)
	_, _, err := w.Apply("u1", wallet.WalletMap{"gold": -80}, "overdraw", 2)
	if !errors.Is(err, wallet.ErrInsufficientBalance) {
		t.Fatalf("want ErrInsufficientBalance, got %v", err)
	}
	// 余额必须不变(回滚)。
	if w.Balance("u1")["gold"] != 50 {
		t.Fatalf("balance changed on rollback: %d", w.Balance("u1")["gold"])
	}
	// 账本不追加。
	if len(w.Ledgers("u1")) != 1 {
		t.Fatalf("ledger should not append on rollback")
	}
}

func TestWallet_PartialMultiCurrencyRollback(t *testing.T) {
	w := wallet.New()
	w.Apply("u1", wallet.WalletMap{"gold": 100, "gem": 2}, "init", 1)
	// gold 充足、gem 不足 → 整体回滚。
	_, _, err := w.Apply("u1", wallet.WalletMap{"gold": -10, "gem": -5}, "mixed", 2)
	if err == nil {
		t.Fatal("want error for gem overdraft")
	}
	bal := w.Balance("u1")
	if bal["gold"] != 100 || bal["gem"] != 2 {
		t.Fatalf("partial rollback failed: %v", bal)
	}
}

func TestWallet_LedgerByID(t *testing.T) {
	w := wallet.New()
	_, l1, _ := w.Apply("u1", wallet.WalletMap{"gold": 100}, "init", 1)
	_, l2, _ := w.Apply("u1", wallet.WalletMap{"gold": 50}, "reward", 2)
	if got := w.LedgerByID("u1", l1.ID); got.Metadata != "init" {
		t.Fatalf("l1 metadata=%s", got.Metadata)
	}
	if got := w.LedgerByID("u1", l2.ID); got.Metadata != "reward" {
		t.Fatalf("l2 metadata=%s", got.Metadata)
	}
	if got := w.LedgerByID("u1", 999); got != nil {
		t.Fatalf("want nil for missing id")
	}
}

func TestWallet_SetBalanceNoLedger(t *testing.T) {
	w := wallet.New()
	w.SetBalance("u1", wallet.WalletMap{"gold": 999})
	if w.Balance("u1")["gold"] != 999 {
		t.Fatalf("setbalance failed")
	}
	if len(w.Ledgers("u1")) != 0 {
		t.Fatalf("setbalance should not create ledger")
	}
}

func TestWallet_EmptyChangeset(t *testing.T) {
	w := wallet.New()
	if _, _, err := w.Apply("u1", wallet.WalletMap{}, "x", 1); err == nil {
		t.Fatal("want error for empty changeset")
	}
}

func TestWallet_NewAccount(t *testing.T) {
	w := wallet.New()
	bal, _, err := w.Apply("new", wallet.WalletMap{"gold": 10}, "init", 1)
	if err != nil {
		t.Fatalf("apply new account: %v", err)
	}
	if bal["gold"] != 10 {
		t.Fatalf("got %d", bal["gold"])
	}
}

func TestWallet_Concurrent(t *testing.T) {
	w := wallet.New()
	w.Apply("u1", wallet.WalletMap{"gold": 10000}, "init", 1)
	var wg sync.WaitGroup
	var ok, fail int64
	var mu sync.Mutex
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := w.Apply("u1", wallet.WalletMap{"gold": -50}, "spend", 2)
			mu.Lock()
			if err == nil {
				ok++
			} else {
				fail++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	// 10000 余额,200 次各扣 50,应恰好全部成功,余额归零。
	if ok != 200 || fail != 0 {
		t.Fatalf("ok=%d fail=%d, want 200/0", ok, fail)
	}
	if w.Balance("u1")["gold"] != 0 {
		t.Fatalf("final balance=%d, want 0", w.Balance("u1")["gold"])
	}
	if len(w.Ledgers("u1")) != 201 {
		t.Fatalf("ledger count=%d, want 201", len(w.Ledgers("u1")))
	}
}
