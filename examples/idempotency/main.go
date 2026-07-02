// idempotency 示例:幂等执行(去重 + 并发合并)。
//
// 演示 pkg/idempotency:同一 key 重复请求只执行一次,并发相同 key 只有一个执行、
// 其余共享结果。典型场景:防止网络重试导致的重复扣款 / 重复发奖。
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/idempotency"
)

func main() {
	store := idempotency.New[string](idempotency.WithTTL(5 * time.Minute))
	defer store.Stop()

	var executions atomic.Int64

	// 模拟一个有副作用的操作(如扣款 + 发奖)。
	grantReward := func(orderID string) func() (string, error) {
		return func() (string, error) {
			executions.Add(1)
			time.Sleep(50 * time.Millisecond) // 模拟 DB 写入
			return "reward-granted:" + orderID, nil
		}
	}

	// 场景 1:同一订单重复提交(串行),只执行一次。
	r1, _, shared1 := store.Do("order-1001", grantReward("order-1001"))
	r2, _, shared2 := store.Do("order-1001", grantReward("order-1001"))
	fmt.Printf("串行重复: r1=%q shared=%v | r2=%q shared=%v\n", r1, shared1, r2, shared2)

	// 场景 2:同一订单并发提交(客户端狂点),只执行一次,其余共享结果。
	var wg sync.WaitGroup
	var sharedCount atomic.Int64
	for range 20 {
		wg.Go(func() {
			_, _, shared := store.Do("order-2002", grantReward("order-2002"))
			if shared {
				sharedCount.Add(1)
			}
		})
	}
	wg.Wait()
	fmt.Printf("并发 20 次: 复用 %d 次(仅 1 次真正执行)\n", sharedCount.Load())

	fmt.Printf("总副作用执行次数 = %d(两个订单各 1 次)\n", executions.Load())
}
