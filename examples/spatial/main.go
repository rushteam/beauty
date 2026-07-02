// spatial 示例:网格空间索引(附近的人)。
//
// 演示 pkg/spatial:Add/Move/Remove 实体,Nearby 范围查询、KNN 最近 N 个。
// 场景:LBS「附近的人」、MMO 兴趣区域(AOI)、大地图分区。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/spatial"
)

func main() {
	// cellSize=100:网格单元边长,建议设为典型查询半径量级。
	ix := spatial.New[string](100)

	// 放置一批玩家(坐标单位可视作米)。
	ix.Add("我", 0, 0)
	ix.Add("小明", 30, 40)  // 距我 50
	ix.Add("小红", 60, 80)  // 距我 100
	ix.Add("小刚", 300, 20) // 距我 ~300
	ix.Add("小李", 5, 5)    // 距我 ~7

	// 场景 1:附近 120 米内的人(排除自己),按距离排序。
	fmt.Println("附近 120 米内的人:")
	for _, e := range ix.Nearby(0, 0, 120, "我") {
		fmt.Printf("  %s (%.0f,%.0f) 距离 %.1f 米\n", e.ID, e.X, e.Y, e.Dist)
	}

	// 场景 2:最近的 2 个人。
	fmt.Println("\n最近的 2 个人:")
	for _, e := range ix.KNN(0, 0, 2, 500, "我") {
		fmt.Printf("  %s 距离 %.1f 米\n", e.ID, e.Dist)
	}

	// 场景 3:玩家移动后重新查询。
	ix.Move("小刚", 10, 10) // 小刚瞬移到附近
	fmt.Println("\n小刚移动到 (10,10) 后,附近 120 米:")
	for _, e := range ix.Nearby(0, 0, 120, "我") {
		fmt.Printf("  %s 距离 %.1f 米\n", e.ID, e.Dist)
	}
}
