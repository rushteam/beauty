// ringbuffer 示例:最近 N 条弹幕。
//
// 演示 pkg/ringbuffer:定长、覆盖最旧、Recent 从新到旧、Slice 从旧到新。
// 场景:最近 N 条弹幕 / 战绩 / 滚动日志。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/ringbuffer"
)

func main() {
	// 直播间只保留最近 5 条弹幕。
	danmaku := ringbuffer.New[string](5)

	for i := 1; i <= 8; i++ {
		danmaku.Push(fmt.Sprintf("弹幕#%d", i))
	}
	// 只剩最近 5 条(#4..#8),#1..#3 已被覆盖。
	fmt.Printf("当前弹幕(从旧到新): %v\n", danmaku.Slice())
	fmt.Printf("最近 3 条(从新到旧): %v\n", danmaku.Recent(3))

	newest, _ := danmaku.Newest()
	oldest, _ := danmaku.Oldest()
	fmt.Printf("最新: %s  最旧: %s  容量: %d/%d\n",
		newest, oldest, danmaku.Len(), danmaku.Cap())
}
