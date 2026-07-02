// momentum 示例:连击 + 热度时间衰减。
//
// 演示 pkg/momentum:连击窗口内递增/断连重置,热度按半衰期指数衰减。
// 场景:直播间刷礼物连击特效 + 实时热度值(自动冷却,无需定时清零)。
package main

import (
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/momentum"
)

func main() {
	tr := momentum.New(
		momentum.WithComboWindow(100*time.Millisecond),
		momentum.WithHalfLife(200*time.Millisecond),
	)

	// 快速连送 5 个礼物 → 连击递增。
	fmt.Println("快速连送礼物:")
	for range 5 {
		st := tr.Hit("room:1", 10)
		fmt.Printf("  第 %d 连击, 热度 = %.1f\n", st.Combo, st.Value)
		time.Sleep(30 * time.Millisecond)
	}

	// 停手超过连击窗口 → 连击断开。
	time.Sleep(150 * time.Millisecond)
	fmt.Printf("\n停手 150ms 后:连击 = %d(已断), 热度 = %.1f(冷却中)\n",
		tr.Combo("room:1"), tr.Value("room:1"))

	// 再送 → 连击从 1 重开,但最高连击保留。
	st := tr.Hit("room:1", 10)
	fmt.Printf("再送一个:连击 = %d, 历史最高连击 = %d\n", st.Combo, st.MaxCombo)

	// 热度随时间进一步衰减。
	time.Sleep(400 * time.Millisecond)
	fmt.Printf("再等 400ms:热度 = %.1f(继续冷却)\n", tr.Value("room:1"))
}
