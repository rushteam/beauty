// cooldown 示例:技能冷却 / 操作限时。
//
// 演示 pkg/cooldown:TryTrigger 原子"检查+触发"、Remaining 剩余 CD、per-action CD。
// 场景:技能 CD / 每日领取 / 发言间隔 / 按钮防连点。
package main

import (
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/cooldown"
)

func main() {
	// 技能默认 CD 300ms。
	cd := cooldown.New(300 * time.Millisecond)
	defer cd.Stop()

	fmt.Println("连续放技能(CD 300ms):")
	for i := 1; i <= 4; i++ {
		if cd.TryTrigger("player1:fireball") {
			fmt.Printf("  第 %d 次: ✓ 释放成功\n", i)
		} else {
			fmt.Printf("  第 %d 次: ✗ 冷却中,还剩 %v\n", i, cd.Remaining("player1:fireball").Truncate(time.Millisecond))
		}
		time.Sleep(120 * time.Millisecond)
	}

	// 等 CD 结束再放。
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("等待后再放: %v\n", cd.TryTrigger("player1:fireball"))

	// per-action CD:不同动作不同冷却。
	fmt.Println("\n不同动作不同 CD:")
	cd.TriggerFor("player1:daily-reward", 24*time.Hour) // 每日领取
	cd.TriggerFor("player1:chat", 3*time.Second)        // 发言间隔
	fmt.Printf("  每日奖励剩余: %v\n", cd.Remaining("player1:daily-reward").Truncate(time.Second))
	fmt.Printf("  发言冷却剩余: %v\n", cd.Remaining("player1:chat").Truncate(time.Second))
}
