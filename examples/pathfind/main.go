// pathfind 示例:网格 A* 寻路。
//
// 演示 pkg/pathfind:在带障碍的网格上求最短路径,可视化输出路线。
// 塔防 / SLG / 点击移动 / 怪物追击的自动寻路。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/pathfind"
)

func main() {
	const w, h = 10, 6
	g := pathfind.NewGrid(w, h)

	// 竖一道墙(留底部一个缺口),制造绕行。
	for y := 0; y < h-1; y++ {
		g.SetBlocked(pathfind.Point{X: 5, Y: y}, true)
	}

	from := pathfind.Point{X: 0, Y: 0}
	to := pathfind.Point{X: 9, Y: 0}
	path := g.FindPath(from, to, pathfind.WithDiagonal(true))
	if path == nil {
		fmt.Println("无路可达")
		return
	}

	// 标记路径,打印地图:S=起点 E=终点 #=墙 *=路径 .=空地
	onPath := make(map[pathfind.Point]bool, len(path))
	for _, p := range path {
		onPath[p] = true
	}
	fmt.Printf("从 %v 到 %v,共 %d 步:\n\n", from, to, len(path))
	for y := range h {
		for x := range w {
			p := pathfind.Point{X: x, Y: y}
			switch {
			case p == from:
				fmt.Print("S ")
			case p == to:
				fmt.Print("E ")
			case g.IsBlocked(p):
				fmt.Print("# ")
			case onPath[p]:
				fmt.Print("* ")
			default:
				fmt.Print(". ")
			}
		}
		fmt.Println()
	}
}
