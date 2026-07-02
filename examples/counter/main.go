// counter 示例:滑动窗口计数 / 时间窗配额。
//
// 演示 pkg/counter:窗口内累计计数(Incr/Count)与配额判断(Allow)。
// 与 pkg/ratelimit(速率)互补:counter 管"窗口内总量"(每日抽卡、分钟弹幕上限)。
package main

import (
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/counter"
)

func main() {
	// 1 分钟窗口的弹幕计数。
	c := counter.New(time.Minute)
	defer c.Stop()

	// 场景 1:累计计数。
	c.Incr("room:1:danmaku", 1)
	c.Incr("room:1:danmaku", 1)
	c.Incr("room:1:danmaku", 3) // 一次批量 +3
	fmt.Printf("room:1 弹幕数(近 1 分钟)= %d\n", c.Count("room:1:danmaku"))

	// 场景 2:配额——每个用户 1 分钟最多发 5 条弹幕。
	fmt.Println("\n用户 u1 连续发弹幕(配额 5):")
	for i := 1; i <= 7; i++ {
		if c.Allow("user:u1:danmaku", 1, 5) {
			fmt.Printf("  第 %d 条: ✓ 通过\n", i)
		} else {
			fmt.Printf("  第 %d 条: ✗ 超出配额\n", i)
		}
	}
}
