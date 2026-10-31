// segment 示例:号段模式分布式 ID(Leaf-segment)。
//
// 演示 pkg/idgen 的号段发号器:每次从后端批发一段连续 ID 到内存,之后内存逐个发号,
// 不依赖时钟、不需分配 node id、ID 紧凑连续。与 Snowflake(examples/idgen)互补。
// 本例用内置内存后端;生产可用 contrib/gorm 的 DB 后端(SegmentStore)。
package main

import (
	"context"
	"fmt"

	"github.com/rushteam/beauty/pkg/idgen"
)

func main() {
	ctx := context.Background()
	// 每次批发 1000 个;阈值 90% 时后台预取下一段,换段无毛刺。
	store := idgen.NewMemorySegmentStore(1000)
	orderID := idgen.NewSegment(store, "order")

	fmt.Println("== 号段发号(tag=order) ==")
	ids := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		id, _ := orderID.Next(ctx)
		ids = append(ids, id)
	}
	fmt.Printf("前 5 个订单号:%v (连续、趋势递增)\n", ids)

	// 不同 tag 互相隔离,各自一条递增序列。
	userID := idgen.NewSegment(store, "user")
	uid, _ := userID.Next(ctx)
	fmt.Printf("user tag 独立序列,首个 = %d\n", uid)

	// 连发 3000 个:跨越多个号段(每段 1000),仍然唯一且递增。
	seen := map[int64]bool{}
	var prev int64 = -1
	mono := true
	for i := 0; i < 3000; i++ {
		id, _ := orderID.Next(ctx)
		if seen[id] || id <= prev {
			mono = false
		}
		seen[id] = true
		prev = id
	}
	fmt.Printf("连发 3000 个(跨多段):全部唯一且单调递增 = %v\n", mono && len(seen) == 3000)
}
