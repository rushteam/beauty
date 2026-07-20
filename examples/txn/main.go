// txn 示例:跨域原子事务——扣钱包 + 写存档,任一失败全回滚。
//
// 演示 pkg/txn 的两阶段提交:用 staging(副本)视图操作,Prepare 暂存、
// Commit 落库、Rollback 丢弃。这里用内存快照模拟:Prepare 前深拷贝主库,
// Commit 把 staging swap 成主库,Rollback 丢弃 staging。
//
// 场景:购买道具——扣金币 + 写背包存档。扣费后存档若失败,金币必须退还。
//
// 跑:go run ./examples/txn
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rushteam/beauty/pkg/txn"
)

// --- 内存 staging:任意 map 的快照-提交模式 ---

// mapStaging 把一个 map[K]V 在事务期间做副本,Commit 时 swap,Rollback 丢弃。
type mapStaging[K comparable, V any] struct {
	mu        *sync.Mutex // 共享主库的锁
	dst       map[K]V     // 主库指针(*map 不行,需 indirection)
	working   map[K]V     // staging 副本
	committed bool
}

func newMapStaging[K comparable, V any](mu *sync.Mutex, dst map[K]V) *mapStaging[K, V] {
	return &mapStaging[K, V]{mu: mu, dst: dst}
}

func (s *mapStaging[K, V]) Prepare(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.working = make(map[K]V, len(s.dst))
	for k, v := range s.dst {
		s.working[k] = v
	}
	return nil
}
func (s *mapStaging[K, V]) Commit(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.dst)
	for k, v := range s.working {
		s.dst[k] = v
	}
	s.committed = true
	return nil
}
func (s *mapStaging[K, V]) Rollback(ctx context.Context) error {
	s.working = nil
	return nil
}

// 业务:钱包(简化为 map) + 背包存档(简化为 map)
type game struct {
	mu     sync.Mutex
	wallet map[string]int64          // userID -> gold
	bag    map[string]map[string]int // userID -> {item -> qty}
}

func newGame() *game {
	return &game{
		wallet: map[string]int64{"alice": 1000},
		bag:    map[string]map[string]int{"alice": {}},
	}
}

func (g *game) buy(ctx context.Context, userID, item string, price, qty int64) error {
	walletStg := newMapStaging[string, int64](&g.mu, g.wallet)
	bagStg := newMapStaging[string, map[string]int](&g.mu, g.bag)

	coord := txn.New()
	coord.Enlist("wallet", walletStg)
	coord.Enlist("bag", bagStg)

	return coord.Run(ctx, func() error {
		// 在 staging 副本上操作(Prepare 已深拷贝)。
		newGold := walletStg.working[userID] - price*qty
		if newGold < 0 {
			return errors.New("insufficient gold") // 触发 Rollback
		}
		walletStg.working[userID] = newGold
		// 背包:道具数量 +qty
		newBag := bagStg.working[userID]
		if newBag == nil {
			newBag = map[string]int{}
		}
		newBag[item] += int(qty)
		bagStg.working[userID] = newBag
		return nil
	})
}

func main() {
	g := newGame()
	ctx := context.Background()

	// 成功案例:买 3 把剑 @50 金币
	if err := g.buy(ctx, "alice", "sword", 50, 3); err != nil {
		panic(err)
	}
	fmt.Printf("after buy: wallet=%d bag=%v\n", g.wallet["alice"], g.bag["alice"])

	// 失败案例:金币不足,应回滚(钱包和背包都不变)
	err := g.buy(ctx, "alice", "shield", 100000, 1)
	fmt.Printf("overpriced buy err: %v\n", err)
	fmt.Printf("after failed buy: wallet=%d bag=%v (unchanged)\n", g.wallet["alice"], g.bag["alice"])
}
