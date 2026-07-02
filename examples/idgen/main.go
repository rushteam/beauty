// idgen 示例:分布式唯一 ID(Snowflake)。
//
// 演示 pkg/idgen:64 位趋势递增 ID、并发唯一、解析出时间/节点/序列。
// 典型场景:对局 ID / 订单号 / 消息序号 / 数据库主键。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/idgen"
)

func main() {
	// 节点 ID = 1(同一部署内每个实例分配唯一值)。
	g, err := idgen.New(1)
	if err != nil {
		panic(err)
	}

	// 连续生成:趋势递增、全局唯一。
	fmt.Println("连续生成 5 个 ID:")
	for range 5 {
		fmt.Printf("  %d\n", g.MustNext())
	}

	// 解析一个 ID 的内部结构。
	id := g.MustNext()
	ts, node, seq := idgen.Parse(id)
	fmt.Printf("\n解析 ID %d:\n", id)
	fmt.Printf("  生成时间 = %s\n", idgen.TimeOf(id, idgen.DefaultEpoch()).Format("2006-01-02 15:04:05.000"))
	fmt.Printf("  节点 ID  = %d\n", node)
	fmt.Printf("  序列号   = %d(相对纪元毫秒 = %d)\n", seq, ts)

	// 两个不同节点同时生成不冲突。
	g2, _ := idgen.New(2)
	fmt.Printf("\n节点1: %d\n节点2: %d(node 位不同,天然不冲突)\n", g.MustNext(), g2.MustNext())
}
