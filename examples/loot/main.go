// loot 示例:加权随机抽卡(含保底)。
//
// 演示 pkg/loot:Alias Method 加权抽取、按稀有度保底(pity)、十连不重复。
// 场景:抽卡 / 开宝箱 / 怪物掉落 / 直播抽奖。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/loot"
)

func main() {
	// 一张卡池:普通 94.3% / 稀有 5% / 史诗 0.7%(权重比例)。
	items := []loot.Item[string]{
		{Value: "普通卡", Weight: 943, Rarity: 1},
		{Value: "稀有卡", Weight: 50, Rarity: 3},
		{Value: "史诗卡", Weight: 7, Rarity: 5},
	}
	tb, err := loot.NewTable(items)
	if err != nil {
		panic(err)
	}

	// 单抽分布(抽 10000 次看比例)。
	count := map[string]int{}
	for range 10000 {
		count[tb.Draw()]++
	}
	fmt.Printf("单抽 10000 次分布: %v\n", count)

	// 保底:连续 90 抽没出史诗(Rarity>=5),第 90 抽必出。
	puller := loot.NewPuller(tb, 90, 5)
	var draws, pityHit int
	for i := 1; ; i++ {
		it, pity := puller.Draw()
		if it.Rarity >= 5 {
			draws = i
			if pity {
				pityHit = i
			}
			break
		}
	}
	if pityHit > 0 {
		fmt.Printf("保底演示: 第 %d 抽出史诗(由保底触发)\n", pityHit)
	} else {
		fmt.Printf("保底演示: 第 %d 抽自然出史诗(未到保底)\n", draws)
	}

	// 十连不重复(从奖励池抽 3 个不同的)。
	rewards := []loot.Item[string]{
		{Value: "金币", Weight: 5}, {Value: "钻石", Weight: 3},
		{Value: "体力", Weight: 2}, {Value: "碎片", Weight: 1},
	}
	rt, _ := loot.NewTable(rewards)
	fmt.Printf("不放回抽 3 个: %v\n", rt.DrawDistinct(3))
}
