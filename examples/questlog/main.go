// questlog 示例:每日任务 / 成就进度。
//
// 演示 pkg/questlog:进度累加、达标可领、前置依赖、领取幂等、重置。
// 场景:每日任务 / 成就 / 活动进度 / 新手引导 / 通行证。
package main

import (
	"fmt"
	"slices"

	"github.com/rushteam/beauty/pkg/questlog"
)

func main() {
	log := questlog.New([]questlog.Quest[string]{
		{ID: "login", Target: 1, Meta: "登录奖励 100 金币"},
		{ID: "kill", Target: 10, Meta: "击杀 10 个怪 → 50 钻石"},
		{ID: "vip", Target: 1, Requires: []string{"login", "kill"}, Meta: "完成日常 → VIP 经验"},
	}, questlog.WithOnClaim(func(owner string, q questlog.Quest[string]) {
		fmt.Printf("  [发奖] %s 领取「%v」\n", owner, q.Meta)
	}))

	const player = "player1"

	// 推进进度。
	log.Advance(player, "login", 1)
	log.Advance(player, "kill", 6)
	fmt.Println("当前任务状态:")
	for _, s := range log.List(player) {
		fmt.Printf("  %-6s %s  %d/%d\n", s.ID, s.Status, s.Progress, s.Target)
	}

	// vip 被锁定(前置未领取)。
	fmt.Printf("\nvip 可领取? %v(前置未完成)\n", slices.Contains(log.Claimable(player), "vip"))

	// 领取已达成的 login。
	fmt.Println("\n领取 login:")
	log.Claim(player, "login")
	// 补齐 kill 并领取。
	log.Advance(player, "kill", 10)
	fmt.Println("领取 kill:")
	log.Claim(player, "kill")

	// 现在 vip 解锁了。
	st, _ := log.StateOf(player, "vip")
	fmt.Printf("\n前置领完后 vip 状态: %s\n", st.Status)
	log.Advance(player, "vip", 1)
	fmt.Println("完成并一键领取所有:")
	fmt.Printf("  领取了: %v\n", log.ClaimAll(player))
}
