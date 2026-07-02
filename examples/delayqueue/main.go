// delayqueue 示例:定点单次延迟触发。
//
// 演示 pkg/delayqueue:Schedule 定时触发、Cancel 取消、改期(重复 Schedule 覆盖)。
// 典型场景:开局倒计时、buff 到期、订单超时取消、匹配超时兜底。
package main

import (
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/delayqueue"
)

func main() {
	q := delayqueue.New()
	defer q.Stop()

	done := make(chan struct{})

	// 场景 1:订单 300ms 未支付则取消。
	q.Schedule("order:1001", 300*time.Millisecond, func() {
		fmt.Println("[300ms] 订单 1001 超时未支付,已取消")
	})

	// 场景 2:开局倒计时,100ms 后开始。
	q.Schedule("match:start", 100*time.Millisecond, func() {
		fmt.Println("[100ms] 匹配成功,对局开始")
	})

	// 场景 3:先排一个 50ms 的踢人任务,但玩家回来了 → 取消。
	q.Schedule("kick:player7", 50*time.Millisecond, func() {
		fmt.Println("不应打印:该任务已被取消")
	})
	q.Cancel("kick:player7")

	// 场景 4:buff 改期——先 80ms,后延长到 400ms(覆盖)。
	q.Schedule("buff:expire", 80*time.Millisecond, func() {
		fmt.Println("不应打印:已被改期覆盖")
	})
	q.Schedule("buff:expire", 400*time.Millisecond, func() {
		fmt.Println("[400ms] buff 到期(改期后)")
		close(done)
	})

	fmt.Printf("已排入 %d 个任务,等待触发...\n", q.Len())

	select {
	case <-done:
		fmt.Println("全部演示完成")
	case <-time.After(2 * time.Second):
		fmt.Println("超时")
	}
}
