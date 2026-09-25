// skiplist 示例:游戏排行榜。
//
// 演示 pkg/foundation/skiplist:有序映射,期望 O(log n) 点查/插删,支持名次与范围查询。
// 场景:排行榜(按分数排序取 Top-K / 查名次)、轻量有序索引、Redis ZSET 式结构。
package main

import (
	"cmp"
	"fmt"

	"github.com/rushteam/beauty/pkg/foundation/skiplist"
)

// 分数在前,便于按分数排序;同分用玩家名去重(保证 key 唯一)。
type score struct {
	points int
	player string
}

func cmpScore(a, b score) int {
	if a.points != b.points {
		return cmp.Compare(a.points, b.points) // 升序,末尾是最高分
	}
	return cmp.Compare(a.player, b.player)
}

func main() {
	board := skiplist.New[score, struct{}](cmpScore)
	data := map[string]int{"Alice": 1500, "Bob": 2300, "Carol": 1800, "Dave": 900, "Eve": 2100}
	for p, s := range data {
		board.Set(score{points: s, player: p}, struct{}{})
	}

	fmt.Println("== 排行榜(分数升序) ==")
	rank := 0
	board.Range(func(k score, _ struct{}) bool {
		rank++
		fmt.Printf("第%d名(升序) %-6s %d\n", rank, k.player, k.points)
		return true
	})

	// Top-3(从最高分往前):用 Max 找末尾,再看名次
	fmt.Println("\n== Top-3 高分 ==")
	total := board.Len()
	for i := 0; i < 3 && i < total; i++ {
		k, _, _ := board.ByRank(total - i) // 名次从 1 开始,total 是最高分
		fmt.Printf("Top%d: %-6s %d\n", i+1, k.player, k.points)
	}

	// 查某玩家名次(比其低分的人数)
	bob := score{points: 2300, player: "Bob"}
	fmt.Printf("\nBob 的名次(升序第几): %d / %d\n", board.Rank(bob), total)

	// 范围查询:分数 >= 1800 的玩家
	fmt.Println("\n== 分数 >= 1800 ==")
	board.RangeFrom(score{points: 1800, player: ""}, func(k score, _ struct{}) bool {
		fmt.Printf("%-6s %d\n", k.player, k.points)
		return true
	})
}
