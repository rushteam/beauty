// crdt 示例:无冲突复制数据类型(多副本最终一致)。
//
// 演示 pkg/foundation/crdt:多副本各自独立更新、乱序交换状态后收敛到同一结果。
// 场景:多端协同、离线优先同步、多活计数、购物车合并。
package main

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rushteam/beauty/pkg/foundation/crdt"
)

func main() {
	// ===== PNCounter:多节点并发点赞计数 =====
	fmt.Println("== PNCounter 多活点赞 ==")
	n1 := crdt.NewPNCounter()
	n2 := crdt.NewPNCounter()
	n1.Inc("node1", 10) // 节点 1 收到 10 个赞
	n2.Inc("node2", 7)  // 节点 2 收到 7 个赞
	n2.Dec("node2", 2)  // 节点 2 有 2 个取消赞
	// 节点 1 通过 JSON 把状态同步给节点 2。
	wire, _ := json.Marshal(n1)
	recv := crdt.NewPNCounter()
	_ = json.Unmarshal(wire, recv)
	n2.Merge(recv)
	fmt.Printf("  合并后总赞数 = %d (10 + 7 - 2)\n\n", n2.Value())

	// ===== ORSet:协同购物车(add 优先于并发 remove) =====
	fmt.Println("== ORSet 协同购物车 ==")
	cartA := crdt.NewORSet[string]("deviceA")
	cartA.Add("iPhone")
	cartA.Add("耳机")

	// 设备 B 从 A 同步了当前状态。
	wire, _ = json.Marshal(cartA)
	cartB := crdt.NewORSet[string]("deviceB")
	_ = json.Unmarshal(wire, cartB)

	// 并发:A 删掉耳机,B 又重新加了耳机。
	cartA.Remove("耳机")
	cartB.Add("耳机")

	// 双向合并后收敛。
	wa, _ := json.Marshal(cartA)
	wb, _ := json.Marshal(cartB)
	ra, rb := crdt.NewORSet[string]("t"), crdt.NewORSet[string]("t")
	_ = json.Unmarshal(wb, ra)
	_ = json.Unmarshal(wa, rb)
	cartA.Merge(ra)
	cartB.Merge(rb)

	items := cartA.Elements()
	sort.Strings(items)
	fmt.Printf("  合并后购物车 = %v (耳机因 add 优先而保留)\n", items)
}
