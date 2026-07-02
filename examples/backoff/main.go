// backoff 示例:指数退避 + 抖动重试。
//
// 演示 pkg/backoff:Duration(attempt) 计算退避序列、Retry 包住可能失败的操作、
// RetryIf 对特定错误不重试。
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
)

func main() {
	// 退避序列(无抖动看清倍率)。
	p := backoff.New(
		backoff.WithBase(100*time.Millisecond),
		backoff.WithFactor(2),
		backoff.WithMax(2*time.Second),
		backoff.WithJitter(backoff.JitterNone),
	)
	fmt.Println("退避序列(base=100ms factor=2 max=2s):")
	for i := range 6 {
		fmt.Printf("  第 %d 次重试等待 %v\n", i, p.Duration(i))
	}

	// Retry:模拟第 3 次才成功的操作。
	fmt.Println("\nRetry(前两次失败):")
	rp := backoff.New(backoff.WithBase(10*time.Millisecond), backoff.WithMaxRetries(5), backoff.WithJitter(backoff.JitterNone))
	var attempt int
	err := rp.Retry(context.Background(), func(ctx context.Context) error {
		attempt++
		if attempt < 3 {
			fmt.Printf("  第 %d 次: 失败\n", attempt)
			return errors.New("temporary")
		}
		fmt.Printf("  第 %d 次: 成功 ✓\n", attempt)
		return nil
	})
	fmt.Printf("  结果: err=%v\n", err)

	// RetryIf:4xx 类错误不重试。
	fmt.Println("\nRetryIf(遇到不可重试错误立即停止):")
	badRequest := errors.New("400 bad request")
	var calls int
	err = rp.RetryIf(context.Background(),
		func(ctx context.Context) error { calls++; return badRequest },
		func(e error) bool { return !errors.Is(e, badRequest) })
	fmt.Printf("  调用 %d 次即停止, err=%v\n", calls, err)
}
