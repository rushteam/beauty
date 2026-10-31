// intervaltree 示例:区间树(时间区间冲突检测)。
//
// 演示 pkg/foundation/intervaltree:高效回答「哪些区间覆盖某点 / 与某区间相交」。
// 场景:会议室/资源预订冲突、排期占用、价格有效期、IP 段归属。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/foundation/intervaltree"
)

func main() {
	// 场景:会议室预订。区间 = [开始分钟, 结束分钟],载荷 = 预订人。
	tr := intervaltree.New[string]()
	tr.Insert(540, 600, "Alice (09:00-10:00)")
	tr.Insert(600, 660, "Bob (10:00-11:00)")
	tr.Insert(630, 720, "Carol (10:30-12:00)")

	fmt.Println("== 新预订冲突检测 ==")
	// 新预订 10:15-10:45 (615-645) 是否与已有冲突?
	start, end := 615, 645
	if tr.Any(int64(start), int64(end)) {
		fmt.Printf("预订 [%d,%d] 冲突!占用方:\n", start, end)
		for _, m := range tr.Overlap(int64(start), int64(end)) {
			fmt.Printf("  - %s\n", m.Value)
		}
	} else {
		fmt.Printf("预订 [%d,%d] 可用\n", start, end)
	}

	fmt.Println("\n== 某时刻谁在用 ==")
	// 10:40 (640 分钟) 哪些会议正在进行?
	for _, m := range tr.Stab(640) {
		fmt.Printf("  10:40 占用中:%s\n", m.Value)
	}

	// 取消 Carol 的预订后再查。
	tr.Remove(630, 720)
	fmt.Printf("\n取消 Carol 后,预订 [%d,%d] 可用? %v\n", start, end, !tr.Any(int64(start), int64(end)))
}
