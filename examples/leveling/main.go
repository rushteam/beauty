// leveling 示例:经验 / 等级曲线。
//
// 演示 pkg/leveling:Gain 加经验算升级、三种曲线(线性/多项式/查表)、满级溢出。
// 场景:角色升级 / 主播等级 / VIP / 亲密度。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/leveling"
)

func main() {
	// 二次曲线:达到 level 级需 100*(level-1)^2 经验,满级 30。
	lv := leveling.New(leveling.Poly(100, 2, 30))

	fmt.Println("升级曲线(前几级累计经验):")
	for l := 1; l <= 5; l++ {
		fmt.Printf("  %d 级: %d\n", l, lv.Curve().CumulativeExp(l))
	}

	// 玩家从 0 经验开始,几次打怪加经验。
	var exp int64
	for _, gain := range []int64{50, 80, 300, 1000} {
		r := lv.Gain(exp, gain)
		exp = r.TotalExp
		msg := fmt.Sprintf("+%d exp → %d 级 (%d/%d)", gain, r.Level, r.CurExp, r.CurExp+r.NextExp)
		if r.LeveledUp {
			msg += fmt.Sprintf("  ⬆ 升 %d 级!", r.LevelsGain)
		}
		fmt.Println("  " + msg)
	}

	// 查表曲线(直接对接策划数值)。
	fmt.Println("\n查表曲线(VIP 等级):")
	vip := leveling.New(leveling.Table([]int64{0, 100, 300, 600, 1000}))
	s := vip.Stat(450)
	fmt.Printf("  充值 450 → VIP%d,距 VIP%d 还差 %d\n", s.Level, s.Level+1, s.NextExp-s.CurExp)
}
