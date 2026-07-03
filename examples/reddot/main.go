// reddot 示例:小红点 / 未读聚合树。
//
// 演示 pkg/reddot:叶子设未读 → 父节点自动聚合,清零向上传播,Children 渲染分类。
// 场景:App"我的"页红点由多来源汇总。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/reddot"
)

func main() {
	tr := reddot.New()

	// 各来源在叶子上设未读。
	tr.Set("me/msg/chat", 3)       // 私聊 3 条
	tr.Set("me/msg/system", 2)     // 系统消息 2 条
	tr.Set("me/friend/request", 5) // 好友申请 5 个
	tr.Set("me/activity", 1)       // 活动红点

	fmt.Printf("「我的」总红点: %d\n", tr.Count("me"))
	fmt.Printf("  消息分类: %d(私聊+系统自动聚合)\n", tr.Count("me/msg"))
	fmt.Printf("  好友分类: %d\n", tr.Count("me/friend"))

	fmt.Println("\n「我的」下各分类:")
	for _, e := range tr.Children("me") {
		dot := ""
		if e.Count > 0 {
			dot = " 🔴"
		}
		fmt.Printf("  %-10s %d%s\n", e.Name, e.Count, dot)
	}

	// 用户点开消息 → 清零该子树,红点向上传播更新。
	fmt.Println("\n点开「消息」(清零 me/msg):")
	tr.Clear("me/msg")
	fmt.Printf("  消息红点: %d\n", tr.Count("me/msg"))
	fmt.Printf("  「我的」总红点: %d(自动减去已读的消息)\n", tr.Count("me"))
}
