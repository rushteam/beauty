// keyedmutex 示例:按 key 的细粒度锁。
//
// 演示 pkg/keyedmutex:同一 key 串行、不同 key 并行。
// 场景:同一账户的扣款必须串行(防超扣),不同账户互不阻塞。
package main

import (
	"fmt"
	"sync"

	"github.com/rushteam/beauty/pkg/keyedmutex"
)

func main() {
	km := keyedmutex.New()

	// 每个账户余额 100,并发各扣 100 次、每次 1。
	balances := map[string]int{"acc:A": 1000, "acc:B": 1000}

	var wg sync.WaitGroup
	for _, acc := range []string{"acc:A", "acc:B"} {
		for range 1000 {
			wg.Go(func() {
				// 同一账户的扣款串行,临界区内的读-改-写安全。
				unlock := km.Lock(acc)
				balances[acc]--
				unlock()
			})
		}
	}
	wg.Wait()

	fmt.Printf("acc:A 余额 = %d(预期 0)\n", balances["acc:A"])
	fmt.Printf("acc:B 余额 = %d(预期 0)\n", balances["acc:B"])
	fmt.Printf("活跃锁数 = %d(用完自动回收,预期 0)\n", km.Len())
}
