// cron-leader 示例:多实例部署下,Cron 只在选主当选的实例上跑。
//
// 演示 pkg/service/cron 的 WithLeaderElector:解决"多实例各跑一遍定时任务"的
// 重复执行问题(重复发奖、重复扣款、重复生成报表)。本例用 pkg/dlock.NewMemory
// 模拟两个"实例"竞选同一个 key;生产环境换成 pkg/infra/etcd.NewDLock(基于真实
// etcd 集群)即可,业务代码不用改。
//
//	// 生产:
//	client, _ := clientv3.New(clientv3.Config{Endpoints: []string{"etcd:2379"}})
//	elector := etcd.NewDLock(client)
//	cron.New(cron.WithLeaderElector(elector, "myservice-cron"), ...)
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/dlock"
	"github.com/rushteam/beauty/pkg/service/cron"
)

func main() {
	// 共享的选举后端:模拟两个实例背后接的是同一个 etcd 集群。
	elector := dlock.NewMemory()

	var runsOnA, runsOnB atomic.Int64
	newInstance := func(name string, counter *atomic.Int64) *cron.Cron {
		return cron.New(
			cron.WithCronHandler("@every 1s", func(ctx context.Context) error {
				n := counter.Add(1)
				fmt.Printf("[%s] 执行第 %d 次(此刻我是 leader)\n", name, n)
				return nil
			}),
			// 两个实例用同一个 elector + 同一个 key → 互斥,只有一个会真正跑任务。
			cron.WithLeaderElector(elector, "myservice-cron"),
		)
	}

	instanceA := newInstance("实例A", &runsOnA)
	instanceB := newInstance("实例B", &runsOnB)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	fmt.Println("启动两个实例,竞选同一个 leader key:")
	go instanceA.Start(ctx)
	go instanceB.Start(ctx)

	<-ctx.Done()
	time.Sleep(50 * time.Millisecond) // 等两边优雅停止

	fmt.Printf("\n实例A 执行次数: %d\n", runsOnA.Load())
	fmt.Printf("实例B 执行次数: %d\n", runsOnB.Load())
	fmt.Println("(任意时刻只有一个实例在跑,不会重复执行同一个任务)")
}
