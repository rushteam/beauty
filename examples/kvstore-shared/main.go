// kvstore-shared 示例:让 counter/cooldown/idempotency 跨实例共享状态。
//
// 演示 pkg/kvstore:用 WithStore 把三个"有状态"原语的状态存到共享 Store,使其从
// "单进程内存"升级为"多实例一致"。本例用内存 Store 模拟(单进程内两个实例共享
// 同一 Store);生产替换成 Redis 实现的 kvstore.Store 即可(见文件末尾的映射说明)。
package main

import (
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/cooldown"
	"github.com/rushteam/beauty/pkg/counter"
	"github.com/rushteam/beauty/pkg/idempotency"
	"github.com/rushteam/beauty/pkg/kvstore"
)

func main() {
	// 一个共享 Store(生产环境是 Redis;这里用内存模拟)。
	store := kvstore.NewMemory()
	defer store.Stop()

	// 模拟两个服务实例,都接同一个 Store。
	fmt.Println("=== counter:跨实例配额 ===")
	c1 := counter.New(time.Minute, counter.WithStore(store))
	c2 := counter.New(time.Minute, counter.WithStore(store))
	defer c1.Stop()
	defer c2.Stop()
	// 用户请求被负载均衡打到不同实例,配额仍统一计。
	c1.Allow("user:1", 1, 3)
	c2.Allow("user:1", 1, 3)
	c1.Allow("user:1", 1, 3)
	ok := c2.Allow("user:1", 1, 3) // 第 4 次,任一实例都拒
	fmt.Printf("  实例2 看到的用户计数: %d,第4次放行=%v(跨实例配额生效)\n", c2.Count("user:1"), ok)

	fmt.Println("\n=== cooldown:跨实例冷却 ===")
	cd1 := cooldown.New(time.Hour, cooldown.WithStore(store))
	cd2 := cooldown.New(time.Hour, cooldown.WithStore(store))
	defer cd1.Stop()
	defer cd2.Stop()
	got1 := cd1.TryTrigger("daily-reward:user:1") // 实例1 领取
	got2 := cd2.TryTrigger("daily-reward:user:1") // 实例2 再领(应失败)
	fmt.Printf("  实例1 领取=%v,实例2 再领=%v(换实例无法重复领)\n", got1, got2)

	fmt.Println("\n=== idempotency:跨实例去重 ===")
	im1 := idempotency.New[string](idempotency.WithStore(store))
	im2 := idempotency.New[string](idempotency.WithStore(store))
	defer im1.Stop()
	defer im2.Stop()
	var executed int
	grant := func() (string, error) { executed++; return "granted", nil }
	im1.Do("pay:order:1", grant)                 // 实例1 处理
	_, _, shared := im2.Do("pay:order:1", grant) // 重试打到实例2
	fmt.Printf("  实际执行次数=%d,实例2 复用结果=%v(重试不重复发放)\n", executed, shared)

	fmt.Println("\n生产替换:实现 kvstore.Store 接口(每个方法对应一条 Redis 命令),")
	fmt.Println("传给 WithStore 即可。命令映射见各方法 doc(Incr→INCRBY、SetNX→SET NX 等)。")
}
