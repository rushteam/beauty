# saga —— 分布式事务 Saga

演示 `pkg/saga` 的顺序正向步骤 + 失败逆序补偿，配合 `wallet` 幂等键保证补偿不重入。

## 运行

```bash
go run ./examples/saga
```

## 说明

- 抽卡流程：扣钻石 → 发卡 → 写记录；示例中发卡服务故障，触发逆序补偿退钻石。
- 每步 `Step(name, action, compensate)`；`Execute` 遇错自动跑已执行步骤的 compensate。
- 正向/补偿分别用不同幂等键（`tx-draw-1` / `tx-refund-1`），`wallet.ApplyTx` 保证重试不会退两次款。
- `WithCompensationRetry` 控制补偿步骤的重试次数。
