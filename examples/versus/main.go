// versus 示例:直播 PK(限时双方对抗计分)。
//
// 演示 pkg/versus:双方阵营 + 倒计时 + 实时累计分 + 到点定胜负 + 事件订阅。
// 复用 fsm(状态)+ stream(事件广播)+ 内建倒计时。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/versus"
)

func main() {
	ended := make(chan versus.Result, 1)
	m := versus.New("pk-101", []string{"主播A", "主播B"},
		versus.WithDuration(300*time.Millisecond),
		versus.WithOnEnd(func(r versus.Result) { ended <- r }))
	defer m.Close()

	// 订阅事件流(实际会推给客户端渲染 PK 进度条)。
	ch, unsub := m.Subscribe(context.Background())
	defer unsub()
	go func() {
		for ev := range ch {
			if ev.Type == versus.EventScoreChanged {
				fmt.Printf("  [%s] %s +%d → A:%d B:%d 领先:%s\n",
					ev.Type, ev.Side, ev.Delta,
					ev.Snapshot.Scores["主播A"], ev.Snapshot.Scores["主播B"], ev.Snapshot.Leader)
			}
		}
	}()

	m.Start()
	fmt.Println("PK 开始(300ms 倒计时)!观众刷礼物折算成分:")

	// 模拟刷礼物打分。
	m.Add("主播A", 100) // 火箭
	m.Add("主播B", 50)  // 小心心 ×50
	m.Add("主播B", 80)  // 反超
	m.Add("主播A", 60)

	r := <-ended
	fmt.Printf("\nPK 结束!比分 %v\n", r.Scores)
	if r.Tie {
		fmt.Println("平局!")
	} else {
		fmt.Printf("胜者:%s 🏆\n", r.Winner)
	}
}
