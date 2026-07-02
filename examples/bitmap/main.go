// bitmap 示例:签到统计。
//
// 演示 pkg/bitmap:每日签到位图、Count 当日人数、And 求交、连续签到天数。
// 场景:千万级用户签到(1 bit/人/天)/ 去重标记 / 权限位。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/bitmap"
)

func main() {
	// 3 天签到,每天一张 bitmap(位 i = 用户 i 是否签到)。
	mon := bitmap.New(1000)
	tue := bitmap.New(1000)
	wed := bitmap.New(1000)

	mon.Set(1)
	mon.Set(2)
	mon.Set(3)
	tue.Set(2)
	tue.Set(3)
	tue.Set(9)
	wed.Set(2)
	wed.Set(3)

	fmt.Printf("周一签到人数: %d\n", mon.Count())
	fmt.Printf("周二签到人数: %d\n", tue.Count())

	// 三天都签到的用户 = 三张图求交。
	allThree := mon.Clone().And(tue).And(wed)
	fmt.Printf("连续三天都签到的用户: %v\n", allThree.Slice())

	// 某用户从末尾往前的连续签到天数。
	days := []*bitmap.Bitmap{mon, tue, wed}
	fmt.Printf("用户 2 连续签到天数: %d\n", bitmap.ConsecutiveFromEnd(days, 2))
	fmt.Printf("用户 9 连续签到天数: %d(周三缺勤)\n", bitmap.ConsecutiveFromEnd(days, 9))

	// 内存估算:1000 万用户一天只需约 1.25 MB。
	fmt.Printf("\n1000 万用户/天 内存 ≈ %d KB\n", 10_000_000/8/1024)
}
