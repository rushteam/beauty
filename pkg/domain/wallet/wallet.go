// Package wallet 提供虚拟货币/积分/库存的账本式钱包:
// 采用"不可变事务日志(append-only ledger) + 当前余额派生"双模型,
// 通过 changeset 差值更新原子校验,避免超扣/超发。
//
// 设计要点:
//   - users.wallet 存当前余额(快读),wallet_ledger 存只追加账本(可审计);
//   - 每次 ApplyWallets 在同一锁内读余额→应用 changeset→校验非负→写余额+追加账本;
//   - changeset 存差值而非绝对值,天然支持 "<0 即超扣" 的原子检查。
//
// 与直接维护余额的区别:账本不可变,可完整审计、支持余额回溯、防篡改;
// 余额是账本的派生快照,读路径 O(1)。
//
// 适用场景:游戏货币、积分、库存数量、任何"高并发增减 + 不可超扣 + 可审计"的账户。
//
// 零值不可用,用 New 构造。Wallet 并发安全。
package wallet

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
)

// Ledger 是一条不可变的事务记录。创建后永不修改,仅追加。
type Ledger struct {
	ID         int64     // 单调递增 ID(由 Wallet 分配)
	OwnerID    string    // 所属账户
	Changeset  WalletMap // 差值:正数=入账,负数=出账
	Metadata   string    // 业务自定义备注(JSON 等任意编码)
	CreateTime int64     // unix nano
}

// WalletMap 是货币到余额的映射。key=货币代号(如 "gold"/"gem"),value=数量。
// 数量可为负(表示出账),但当前余额不允许为负。
type WalletMap map[string]int64

// Account 是一个账户的运行时状态:当前余额(账本派生)+ 历史账本引用。
type Account struct {
	Balance WalletMap // 当前余额(只读快照)
	ledgers []*Ledger // 完整账本(追加,不删)
}

// Wallet 管理所有账户的余额与账本。
type Wallet struct {
	mu       sync.Mutex
	accounts map[string]*Account
	seq      atomic.Int64        // ledger ID 生成器
	txIndex  map[string]txResult // txID → 首次成功结果(幂等去重,append-only)
}

// txResult 缓存一次成功 ApplyTx 的结果,用于同 txID 重放。
type txResult struct {
	affected WalletMap
	ledgerID int64
}

// ErrInsufficientBalance 余额不足(超扣)。Changeset 不会被应用。
var ErrInsufficientBalance = errors.New("wallet: insufficient balance")

// ErrEmptyTxID ApplyTx 传入空 txID。
var ErrEmptyTxID = errors.New("wallet: empty txID")

// New 创建空钱包。
func New() *Wallet {
	w := &Wallet{
		accounts: make(map[string]*Account),
		txIndex:  make(map[string]txResult),
	}
	return w
}

// Apply 对 owner 应用一个 changeset,原子校验非负后写入余额并追加账本。
// 返回应用后的新余额(该 changeset 涉及的货币)。失败时余额与账本不变。
//
// 流程(参考 ApplyWallets 语义):
//  1. 读当前余额
//  2. 逐项应用 changeset 计算新值
//  3. 任一项 <0 → ErrInsufficientBalance,回滚
//  4. 写余额 + 追加账本(同一锁内原子完成)
func (w *Wallet) Apply(ownerID string, changeset WalletMap, metadata string, now int64) (WalletMap, *Ledger, error) {
	if len(changeset) == 0 {
		return nil, nil, errors.New("wallet: empty changeset")
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	acc := w.accounts[ownerID]
	if acc == nil {
		acc = &Account{Balance: WalletMap{}}
		w.accounts[ownerID] = acc
	}

	// 计算新余额。
	newBal := make(WalletMap, len(changeset))
	affected := make(WalletMap, len(changeset))
	for cur, delta := range changeset {
		newVal := acc.Balance[cur] + delta
		if newVal < 0 {
			return nil, nil, fmt.Errorf("%w: %s want %d, have %d", ErrInsufficientBalance, cur, delta, acc.Balance[cur])
		}
		newBal[cur] = newVal
		affected[cur] = newVal
	}

	// 提交:写余额 + 追加账本(原子)。
	maps.Copy(acc.Balance, newBal)
	l := &Ledger{
		ID:         w.seq.Add(1),
		OwnerID:    ownerID,
		Changeset:  copyMap(changeset),
		Metadata:   metadata,
		CreateTime: now,
	}
	acc.ledgers = append(acc.ledgers, l)
	return affected, l, nil
}

// ApplyTx 是带幂等键的 Apply:同一 txID 重复调用只应用一次,后续调用返回首次
// 成功的结果(相同 affected + 同一账本)而不再改动余额。用于防止网络重试 /
// 客户端重发导致的重复扣款、重复发奖。
//
// 语义:
//   - txID 为空 → ErrEmptyTxID;
//   - txID 首次出现 → 等价 Apply,成功后记录结果,返回 (affected, ledger, false);
//   - txID 已成功过 → 不执行,返回缓存的 (affected, ledger, true);
//   - 首次执行失败(如余额不足)→ 不记录 txID,同 txID 下次仍可重试。
//
// 返回值 replayed 表示本次是否为重放(true=未真正扣款,复用首次结果)。
// 幂等索引随 Wallet 常驻内存(append-only,与账本同生命周期);若需按 TTL
// 淘汰,请在业务层用 pkg/idempotency 包住 ApplyTx。
func (w *Wallet) ApplyTx(txID, ownerID string, changeset WalletMap, metadata string, now int64) (affected WalletMap, ledger *Ledger, replayed bool, err error) {
	if txID == "" {
		return nil, nil, false, ErrEmptyTxID
	}
	if len(changeset) == 0 {
		return nil, nil, false, errors.New("wallet: empty changeset")
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	// 已成功过:重放首次结果。
	if prev, ok := w.txIndex[txID]; ok {
		l := w.ledgerByIDLocked(ownerID, prev.ledgerID)
		return copyMap(prev.affected), l, true, nil
	}

	// 首次:内联 Apply 的核心逻辑(已持锁,不能再调 Apply 以免死锁)。
	acc := w.accounts[ownerID]
	if acc == nil {
		acc = &Account{Balance: WalletMap{}}
		w.accounts[ownerID] = acc
	}
	newBal := make(WalletMap, len(changeset))
	aff := make(WalletMap, len(changeset))
	for cur, delta := range changeset {
		newVal := acc.Balance[cur] + delta
		if newVal < 0 {
			// 失败不记录 txID,允许重试。
			return nil, nil, false, fmt.Errorf("%w: %s want %d, have %d", ErrInsufficientBalance, cur, delta, acc.Balance[cur])
		}
		newBal[cur] = newVal
		aff[cur] = newVal
	}
	maps.Copy(acc.Balance, newBal)
	l := &Ledger{
		ID:         w.seq.Add(1),
		OwnerID:    ownerID,
		Changeset:  copyMap(changeset),
		Metadata:   metadata,
		CreateTime: now,
	}
	acc.ledgers = append(acc.ledgers, l)
	w.txIndex[txID] = txResult{affected: copyMap(aff), ledgerID: l.ID}
	return aff, l, false, nil
}

// ledgerByIDLocked 在已持锁时按 ID 查账本拷贝。不存在返回 nil。
func (w *Wallet) ledgerByIDLocked(ownerID string, id int64) *Ledger {
	acc := w.accounts[ownerID]
	if acc == nil {
		return nil
	}
	for _, l := range acc.ledgers {
		if l.ID == id {
			cp := *l
			return &cp
		}
	}
	return nil
}

// Balance 返回 owner 当前余额的拷贝。不存在则返回空 map。
func (w *Wallet) Balance(ownerID string) WalletMap {
	w.mu.Lock()
	defer w.mu.Unlock()
	acc := w.accounts[ownerID]
	if acc == nil {
		return WalletMap{}
	}
	return copyMap(acc.Balance)
}

// Ledgers 返回 owner 的账本切片(按时间顺序)。不存在返回 nil。
// 注意:返回的是内部切片的拷贝,修改不影响账本。
func (w *Wallet) Ledgers(ownerID string) []Ledger {
	w.mu.Lock()
	defer w.mu.Unlock()
	acc := w.accounts[ownerID]
	if acc == nil {
		return nil
	}
	out := make([]Ledger, len(acc.ledgers))
	for i, l := range acc.ledgers {
		out[i] = *l
	}
	return out
}

// LedgerByID 按 ID 查单条账本。不存在返回 nil。
func (w *Wallet) LedgerByID(ownerID string, id int64) *Ledger {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ledgerByIDLocked(ownerID, id)
}

// Accounts 返回所有 ownerID(快照)。用于遍历或导出。
func (w *Wallet) Accounts() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Collect(maps.Keys(w.accounts))
}

// SetBalance 直接覆盖 owner 的余额(用于从 DB 全量加载初始快照)。
// 不产生账本——仅用于启动时恢复,运行时请用 Apply。
func (w *Wallet) SetBalance(ownerID string, bal WalletMap) {
	w.mu.Lock()
	defer w.mu.Unlock()
	acc := w.accounts[ownerID]
	if acc == nil {
		acc = &Account{Balance: WalletMap{}}
		w.accounts[ownerID] = acc
	}
	acc.Balance = make(WalletMap, len(bal))
	maps.Copy(acc.Balance, bal)
}

func copyMap(m WalletMap) WalletMap {
	out := make(WalletMap, len(m))
	maps.Copy(out, m)
	return out
}
