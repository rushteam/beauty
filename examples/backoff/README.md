# backoff —— 退避重试

演示 `pkg/backoff` 的指数退避序列、`Retry` 自动重试与 `RetryIf` 按错误类型决定是否继续。

## 运行

```bash
go run ./examples/backoff
```

## 说明

- `Duration(attempt)` 计算第 N 次重试等待时间（base × factor，带上限 max）。
- `Retry` 包住可能失败的操作，示例模拟第 3 次才成功。
- `RetryIf` 传入 `retryable(err)` 谓词；遇到 `400 bad request` 等不可重试错误立即停止。
- 生产常用 `JitterFull` 抖动避免惊群；示例用 `JitterNone` 便于看清序列。
