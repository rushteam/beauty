# cron-leader —— 定时任务 + 选主

演示多实例部署下 Cron 只在 leader 实例执行，避免重复发奖、重复扣款等问题。

## 运行

```bash
go run ./examples/cron-leader
```

运行约 3 秒后退出，打印两个实例各自的执行次数（合计 ≈ 3，不会翻倍）。

## 说明

- `cron.WithLeaderElector(elector, "myservice-cron")` 用分布式锁竞选 leader，只有当选实例跑 `@every 1s` 任务。
- 示例用 `dlock.NewMemory()` 模拟共享选举后端；生产换成 `pkg/infra/etcd.NewDLock(client)`，业务代码不变。
- 两个实例共享同一个 elector 和 key，任意时刻只有一个在打印「此刻我是 leader」。
