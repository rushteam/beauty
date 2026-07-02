// tally 示例:高频累计聚合 + 批量刷写。
//
// 演示 pkg/tally:海量小额 +1 在内存聚合,定时合并成一批交给 flush 一次性处理。
// 直播间点赞/刷礼物量级——逐笔写库会打爆下游,tally 把"高频写"削成"低频批量写"。
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/tally"
)

func main() {
	var flushes int
	// flush 回调:这里只打印,实际会批量写库 / 推送。
	t := tally.New(func(ctx context.Context, batch map[string]int64) {
		flushes++
		fmt.Printf("  [flush #%d] 批量落地 %d 个 key: %v\n", flushes, len(batch), batch)
	}, tally.WithFlushInterval(50*time.Millisecond))

	// 模拟 3 个直播间的高频点赞:10000 次 Add。
	fmt.Println("模拟 10000 次高频点赞...")
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			for range 100 {
				t.Add("room:1:like", 1)
				time.Sleep(time.Millisecond) // 拉开时间让多次 flush 发生
			}
		})
	}
	wg.Wait()
	t.Stop() // 含最后一次 flush

	fmt.Printf("\n10000 次 Add 只触发了 %d 次批量 flush(写放大被削平)\n", flushes)
}
