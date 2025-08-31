package xgo

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// 示例：基本使用 - 简单的协程池任务提交
func ExamplePool_Go() {
	pool := New()
	defer pool.Close()

	var wg sync.WaitGroup
	wg.Add(3)

	// 提交任务到协程池
	pool.Go(func() {
		defer wg.Done()
		fmt.Println("任务 1 完成")
	})

	pool.Go(func() {
		defer wg.Done()
		fmt.Println("任务 2 完成")
	})

	pool.Go(func() {
		defer wg.Done()
		fmt.Println("任务 3 完成")
	})

	wg.Wait()
	fmt.Println("所有任务完成")
}

// 示例：WaitGroup - 批量任务管理
func ExamplePool_NewWaitGroup() {
	pool := New()
	defer pool.Close()

	// 创建 WaitGroup 来管理一组任务
	wg := pool.NewWaitGroup()

	// 提交多个任务到同一个 WaitGroup
	for i := 0; i < 3; i++ {
		taskID := i
		wg.Go(func() {
			fmt.Printf("任务 %d 完成\n", taskID)
		})
	}

	// 等待所有任务完成
	wg.Wait()
	fmt.Println("所有任务完成")
}

// 示例：ErrorGroup - 错误收集和快速失败
func ExamplePool_NewErrorGroup() {
	pool := New()
	defer pool.Close()

	eg := pool.NewErrorGroup()

	// 提交多个可能失败的任务
	eg.Go(func() error {
		return nil // 成功
	})

	eg.Go(func() error {
		return fmt.Errorf("任务失败") // 失败
	})

	eg.Go(func() error {
		return nil // 成功
	})

	// 等待所有任务完成并获取第一个错误
	if err := eg.Wait(); err != nil {
		fmt.Printf("发生错误: %v\n", err)
	}
	// Output: 发生错误: 任务失败
}

// 示例：带上下文的任务控制
func ExampleWaitGroup_GoWithContext() {
	pool := New()
	defer pool.Close()

	// 创建带超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*50)
	defer cancel()

	wg := pool.NewWaitGroup()
	wg.GoWithContext(ctx, func(ctx context.Context) {
		select {
		case <-ctx.Done():
			fmt.Println("任务被取消:", ctx.Err())
		case <-time.After(time.Millisecond * 100):
			fmt.Println("任务完成")
		}
	})

	wg.Wait()
	// Output: 任务被取消: context deadline exceeded
}

// 示例：ErrorGroup 快速失败机制
func ExamplePool_NewErrorGroupWithContext() {
	pool := New()
	defer pool.Close()

	// 创建带上下文的 ErrorGroup，支持快速失败
	eg, _ := pool.NewErrorGroupWithContext(context.Background())

	// 提交一个会失败的任务
	eg.Go(func() error {
		return fmt.Errorf("关键任务失败")
	})

	// 等待结果
	if err := eg.Wait(); err != nil {
		fmt.Printf("ErrorGroup 失败: %v\n", err)
	}
	// Output: ErrorGroup 失败: 关键任务失败
}

// 示例：全局便捷函数
func ExampleNewWaitGroup() {
	// 使用全局默认协程池创建 WaitGroup
	wg := NewWaitGroup()

	// 提交任务
	wg.Go(func() {
		fmt.Println("全局任务 1")
	})

	wg.Go(func() {
		fmt.Println("全局任务 2")
	})

	// 等待完成
	wg.Wait()
	fmt.Println("全局任务完成")
}

// 示例：全局 ErrorGroup
func ExampleNewErrorGroup() {
	// 使用全局默认协程池
	eg := NewErrorGroup()

	// 模拟批量处理，其中一个失败
	eg.Go(func() error {
		return nil // 成功
	})

	eg.Go(func() error {
		return fmt.Errorf("处理失败")
	})

	// 等待所有任务完成
	if err := eg.Wait(); err != nil {
		fmt.Printf("批量处理失败: %v\n", err)
	} else {
		fmt.Println("所有处理成功")
	}
	// Output: 批量处理失败: 处理失败
}

// 示例：自定义 panic 处理
func ExampleWithPanicHandler() {
	pool := New(WithPanicHandler(func(taskName string, panicValue any, stack []byte) {
		// 这里可以记录日志、发送告警等
		// 为了示例输出的一致性，这里不打印 panic 信息
	}))
	defer pool.Close()

	wg := pool.NewWaitGroup()
	wg.Go(func() {
		panic("模拟错误")
	})

	wg.Wait()
	fmt.Println("即使发生 panic，程序仍然继续运行")
	// Output: 即使发生 panic，程序仍然继续运行
}

// 示例：协程池配置
func ExampleNew() {
	pool := New(
		WithSetCap(10),        // 最大 10 个 worker
		WithScaleThreshold(3), // 当待处理任务 >= 3 时创建新 worker
	)
	defer pool.Close()

	fmt.Println("协程池创建成功")
	// Output: 协程池创建成功
}

// 示例：优雅关闭
func ExamplePool_Close() {
	pool := New()

	// 提交一些任务
	wg := pool.NewWaitGroup()
	wg.Go(func() {
		fmt.Println("任务执行中...")
	})

	wg.Wait()

	// 关闭协程池
	pool.Close()
	fmt.Println("协程池已优雅关闭")
	// Output:
	// 任务执行中...
	// 协程池已优雅关闭
}
