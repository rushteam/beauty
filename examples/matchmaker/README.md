# matchmaker —— 匹配系统

演示 `pkg/game/matchmaker` 按属性（region、skill）分桶匹配，凑齐人数后回调组队。

## 运行

```bash
go run ./examples/matchmaker
```

```bash
curl "http://localhost:8287/queue?user=u1&region=eu&skill=1000"
curl "http://localhost:8287/queue?user=u2&region=eu&skill=1050"
curl "http://localhost:8287/stats"
```

## 说明

- `Add(ticket, mode, bucket)` 入队；ticket 带 `Properties`（String/Numeric）和 `MinCount`/`MaxCount`。
- 匹配器按 bucket 分组、skill 排序贪心凑队；`WithTickInterval` 控制扫描频率，`WithMaxWaitSec` 超时策略。
- 匹配成功触发回调，示例打印 team 成员并累加 `matched` 计数。
