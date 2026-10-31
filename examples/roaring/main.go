// roaring 示例:Roaring 压缩位图。
//
// 演示 pkg/foundation/roaring:海量稀疏整数集的内存杀手 + 集合运算 + 序列化。
// 场景:用户标签/圈选、布尔列索引、去重 ID 集、人群交并差。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/foundation/roaring"
)

func main() {
	// 场景:两个人群(标签)做圈选运算。
	vip := roaring.Of(1, 5, 9, 100000, 100001, 100002)
	active := roaring.Of(5, 9, 42, 100001, 300000)

	fmt.Println("== 人群圈选(集合运算) ==")
	fmt.Printf("VIP 且活跃(交集):      %v\n", roaring.And(vip, active).ToArray())
	fmt.Printf("VIP 或活跃(并集):      %v\n", roaring.Or(vip, active).ToArray())
	fmt.Printf("VIP 但不活跃(差集):    %v\n", roaring.AndNot(vip, active).ToArray())
	fmt.Printf("VIP 人数: %d\n\n", vip.Len())

	// 场景:大段连续 ID(如 1..1_000_000 全选)—— RunOptimize 后内存极小。
	big := roaring.New()
	for i := uint32(0); i < 1_000_000; i++ {
		big.Add(i)
	}
	before, _ := big.MarshalBinary()
	big.RunOptimize()
	after, _ := big.MarshalBinary()
	fmt.Println("== 连续大段 + RunOptimize ==")
	fmt.Printf("百万连续 ID 序列化:RunOptimize 前 %d 字节 → 后 %d 字节\n\n", len(before), len(after))

	// 场景:持久化 / 跨节点传输(序列化往返)。
	data, _ := vip.MarshalBinary()
	var restored roaring.Bitmap
	_ = restored.UnmarshalBinary(data)
	fmt.Println("== 序列化往返 ==")
	fmt.Printf("序列化 %d 字节,反序列化后 Contains(100000)=%v\n", len(data), restored.Contains(100000))
}
