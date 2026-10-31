// tdigest 示例:流式分位数估计(p50/p90/p99/p999)。
//
// 演示 pkg/foundation/sketch 的 TDigest:常量内存估计尾延迟,可合并(分布式汇总)。
// 场景:接口延迟监控、SLO 统计、实时风控阈值。
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/rushteam/beauty/pkg/foundation/sketch"
)

func main() {
	// 场景:统计接口响应延迟分布。
	td := sketch.NewTDigest(100)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		// 大多数请求很快(~20ms),少量慢请求(长尾)。
		lat := rng.ExpFloat64() * 20
		if rng.Float64() < 0.01 {
			lat += 500 // 1% 的慢请求
		}
		td.Add(lat)
	}
	fmt.Println("== 接口延迟分位数(单位 ms) ==")
	for _, q := range []float64{0.5, 0.9, 0.99, 0.999} {
		fmt.Printf("p%-4.1f = %.1f ms\n", q*100, td.Quantile(q))
	}
	fmt.Printf("样本数 %.0f, min=%.1f max=%.1f\n\n", td.Count(), td.Min(), td.Max())

	// 场景:多实例分布式汇总 —— 各实例序列化上报,汇总端合并出全局 p99。
	nodeA := sketch.NewTDigest(100)
	nodeB := sketch.NewTDigest(100)
	for i := 0; i < 50000; i++ {
		nodeA.Add(rng.ExpFloat64() * 20)
		nodeB.Add(rng.ExpFloat64()*20 + 10) // B 节点整体偏慢
	}
	agg := sketch.NewTDigest(100)
	for _, nd := range []*sketch.TDigest{nodeA, nodeB} {
		wire, _ := json.Marshal(nd)
		recv := sketch.NewTDigest(100)
		_ = json.Unmarshal(wire, recv)
		agg.Merge(recv)
	}
	fmt.Println("== 分布式汇总(A+B 两实例) ==")
	fmt.Printf("全局 p99 = %.1f ms (合并 %.0f 个样本)\n", agg.Quantile(0.99), agg.Count())
}
