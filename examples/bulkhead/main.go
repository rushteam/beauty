// bulkhead 示例:隔离舱并发隔离。
//
// 演示 pkg/resilience/bulkhead:给某类调用划独立并发配额,满了快速失败,
// 防止慢依赖把调用方协程全部堵死(资源耗尽型雪崩)。
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/resilience/bulkhead"
)

func main() {
	// 下游依赖:最多允许 3 个并发,再多排队 2 个,超出立即拒绝。
	bh := bulkhead.New(3, bulkhead.WithQueue(2), bulkhead.WithMaxWait(50*time.Millisecond))

	var ok, rejected int32
	var wg sync.WaitGroup
	// 10 个请求同时打进来。
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := bh.Do(context.Background(), func() error {
				time.Sleep(30 * time.Millisecond) // 模拟慢下游
				return nil
			})
			if errors.Is(err, bulkhead.ErrFull) || errors.Is(err, bulkhead.ErrAcquireTimeout) {
				atomic.AddInt32(&rejected, 1)
			} else {
				atomic.AddInt32(&ok, 1)
			}
		}(i)
	}
	wg.Wait()

	fmt.Println("== 隔离舱(并发3 + 队列2) ==")
	fmt.Printf("10 个并发请求:成功 %d,被快速拒绝 %d\n", ok, rejected)
	fmt.Printf("累计拒绝数(含超时/取消)= %d\n", bh.Rejected())
	fmt.Println("—— 没有隔离舱时,10 个请求会全部堵在慢下游;有了它,只放行配额内的,其余立即失败。")
}
