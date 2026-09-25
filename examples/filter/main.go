// filter 示例:布隆 / 布谷鸟过滤器。
//
// 演示 pkg/foundation/filter:用极小内存做"一定不存在 / 可能存在"判定。
// 场景:缓存穿透前置判断、海量去重、URL 判重。Cuckoo 额外支持删除。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/foundation/filter"
)

func main() {
	// ===== 布隆过滤器:缓存穿透防护 =====
	// 把 DB 里已存在的 key 全部灌进布隆;查询前先问布隆,"一定不存在"直接挡掉,不打 DB。
	bloom := filter.NewBloom(100000, 0.01) // 预期 10w key,目标误判率 1%
	for _, id := range []string{"user:1", "user:2", "user:1000"} {
		bloom.AddString(id)
	}
	fmt.Println("== 布隆过滤器(缓存穿透防护) ==")
	fmt.Printf("user:1   存在? %v (可能存在→查缓存/DB)\n", bloom.TestString("user:1"))
	fmt.Printf("user:999 存在? %v (一定不存在→直接挡掉)\n", bloom.TestString("user:999"))
	fmt.Printf("当前填充率 %.4f,估计误判率 %.4f\n\n", bloom.FillRatio(), bloom.EstimatedFalsePositiveRate())

	// ===== 布谷鸟过滤器:支持删除的去重 =====
	cuckoo := filter.NewCuckoo(100000)
	cuckoo.AddString("spam@evil.com")
	cuckoo.AddString("ok@good.com")
	fmt.Println("== 布谷鸟过滤器(支持删除) ==")
	fmt.Printf("spam@evil.com 命中? %v\n", cuckoo.ContainsString("spam@evil.com"))
	cuckoo.DeleteString("spam@evil.com") // 布隆做不到删除
	fmt.Printf("删除后再查:      %v\n", cuckoo.ContainsString("spam@evil.com"))
	fmt.Printf("负载因子 %.4f,元素数 %d\n", cuckoo.LoadFactor(), cuckoo.Len())
}
