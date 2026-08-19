# XGo - 安全的 Go 协程池

XGo 是一个功能完整、安全可靠的 Go 协程池实现，提供了三种任务管理方式：基本任务提交、WaitGroup 批量管理和 ErrorGroup 错误收集。

## 核心特性

- 🚀 **高性能**：基于 worker pool 模式，复用协程减少创建开销
- 🛡️ **Panic 安全**：自动捕获和处理 panic，不会导致程序崩溃
- 🎯 **灵活控制**：支持协程数量限制、动态扩缩容
- 📦 **批量管理**：WaitGroup 支持批量任务等待
- ⚡ **错误收集**：ErrorGroup 支持错误收集和快速失败
- 🔄 **上下文控制**：完整的 context.Context 支持
- 🔒 **优雅关闭**：支持优雅关闭和超时关闭

## 快速开始

### 基本使用

```go
import "github.com/rushteam/beauty/pkg/foundation/xgo"

// 创建协程池
pool := xgo.New()
defer pool.Close()

// 提交任务
pool.Go(func() {
    fmt.Println("Hello, XGo!")
})
```

### 全局便捷函数

```go
// 使用全局默认协程池
xgo.SafeGo(func() {
    fmt.Println("Safe goroutine")
})
```

## 三种任务管理方式

### 1. 基本任务提交 - Go()

适用于简单的 fire-and-forget 任务：

```go
pool := xgo.New()
defer pool.Close()

pool.Go(func() {
    // 执行任务
    fmt.Println("Task completed")
})
```

### 2. WaitGroup - 批量任务管理

适用于需要等待一组任务完成的场景：

```go
pool := xgo.New()
defer pool.Close()

wg := pool.NewWaitGroup()

// 提交多个任务
for i := 0; i < 10; i++ {
    taskID := i
    wg.Go(func() {
        fmt.Printf("Task %d completed\n", taskID)
    })
}

// 等待所有任务完成
wg.Wait()
fmt.Println("All tasks completed")
```

### 3. ErrorGroup - 错误收集和快速失败

适用于需要收集错误或快速失败的场景：

```go
pool := xgo.New()
defer pool.Close()

eg := pool.NewErrorGroup()

// 提交可能失败的任务
eg.Go(func() error {
    // 业务逻辑
    if someCondition {
        return fmt.Errorf("task failed")
    }
    return nil
})

// 等待并获取第一个错误
if err := eg.Wait(); err != nil {
    fmt.Printf("Error occurred: %v\n", err)
}
```

## 高级功能

### 上下文控制

```go
ctx, cancel := context.WithTimeout(context.Background(), time.Second)
defer cancel()

wg := pool.NewWaitGroup()
wg.GoWithContext(ctx, func(ctx context.Context) {
    select {
    case <-ctx.Done():
        fmt.Println("Task cancelled")
        return
    default:
        // 执行任务
    }
})
wg.Wait()
```

### ErrorGroup 快速失败

```go
eg, ctx := pool.NewErrorGroupWithContext(context.Background())

// 当任何任务失败时，其他任务会被自动取消
eg.Go(func() error {
    return fmt.Errorf("critical error") // 触发快速失败
})

eg.GoWithContext(ctx, func(ctx context.Context) error {
    select {
    case <-ctx.Done():
        return ctx.Err() // 被取消
    default:
        // 正常执行
        return nil
    }
})

err := eg.Wait() // 返回第一个错误
```

### 自定义配置

```go
pool := xgo.New(
    xgo.WithSetCap(100),                    // 最大 100 个 worker
    xgo.WithScaleThreshold(5),              // 当待处理任务 >= 5 时创建新 worker
    xgo.WithPanicHandler(func(taskName string, panicValue any, stack []byte) {
        // 自定义 panic 处理
        log.Printf("Panic in task [%s]: %v\n%s", taskName, panicValue, stack)
    }),
)
defer pool.Close()
```

## 实际应用场景

### 批量数据处理

```go
func ProcessUsers(users []User) error {
    pool := xgo.New(xgo.WithSetCap(20))
    defer pool.Close()
    
    wg := pool.NewWaitGroup()
    
    for _, user := range users {
        user := user
        wg.Go(func() {
            processUser(user)
        })
    }
    
    wg.Wait()
    return nil
}
```

### 批量 API 调用

```go
func CallAPIs(urls []string) error {
    eg := xgo.NewErrorGroup()
    
    for _, url := range urls {
        url := url
        eg.Go(func() error {
            resp, err := http.Get(url)
            if err != nil {
                return err
            }
            defer resp.Body.Close()
            
            if resp.StatusCode != 200 {
                return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
            }
            return nil
        })
    }
    
    return eg.Wait() // 返回第一个错误
}
```

### 微服务并发调用

```go
func CallServices(ctx context.Context) error {
    eg, ctx := xgo.NewErrorGroupWithContext(ctx)
    
    eg.GoWithContext(ctx, func(ctx context.Context) error {
        return callUserService(ctx)
    })
    
    eg.GoWithContext(ctx, func(ctx context.Context) error {
        return callOrderService(ctx)
    })
    
    return eg.Wait() // 任何服务失败都会快速返回
}
```

## API 参考

### Pool 接口

```go
type Pool interface {
    Go(f func())
    GoWithContext(ctx context.Context, f func(ctx context.Context))
    NewWaitGroup() *WaitGroup
    NewErrorGroup() *ErrorGroup
    NewErrorGroupWithContext(ctx context.Context) (*ErrorGroup, context.Context)
    Workers() int32
    PendingTasks() int
    Close() error
    CloseWithTimeout(timeout time.Duration) error
}
```

### WaitGroup 方法

```go
func (w *WaitGroup) Go(f func())
func (w *WaitGroup) GoWithContext(ctx context.Context, f func(ctx context.Context))
func (w *WaitGroup) Wait()
func (w *WaitGroup) Add(delta int)    // 高级用法
func (w *WaitGroup) Done()            // 高级用法
```

### ErrorGroup 方法

```go
func (eg *ErrorGroup) Go(f func() error)
func (eg *ErrorGroup) GoWithContext(ctx context.Context, f func(ctx context.Context) error)
func (eg *ErrorGroup) Wait() error
func (eg *ErrorGroup) Context() context.Context
```

### 全局函数

```go
func SafeGo(f func())
func SafeGoWithContext(ctx context.Context, f func(ctx context.Context))
func NewWaitGroup() *WaitGroup
func NewErrorGroup() *ErrorGroup
func NewErrorGroupWithContext(ctx context.Context) (*ErrorGroup, context.Context)
```

## 配置选项

```go
func WithSetCap(cap int32) Option                    // 设置最大 worker 数量
func WithScaleThreshold(threshold int) Option        // 设置扩容阈值
func WithPanicHandler(f func(string, any, []byte)) Option  // 设置 panic 处理函数
```

## 最佳实践

1. **选择合适的模式**：
   - 简单任务用 `Go()`
   - 需要等待的批量任务用 `WaitGroup`
   - 需要错误处理的任务用 `ErrorGroup`

2. **合理设置参数**：
   - `WithSetCap()` 建议设置为 CPU 核数的 2-10 倍
   - `WithScaleThreshold()` 建议设置为 2-5

3. **资源管理**：
   - 总是调用 `defer pool.Close()` 确保资源释放
   - 对于长期运行的服务，考虑使用全局协程池

4. **错误处理**：
   - 使用 `ErrorGroup` 处理可能失败的批量任务
   - 自定义 `PanicHandler` 进行错误监控和告警

## 性能特点

- **内存效率**：复用协程，减少创建/销毁开销
- **调度优化**：控制并发数量，减少调度器压力
- **资源可控**：支持最大协程数限制，防止资源耗尽
- **快速失败**：ErrorGroup 支持快速失败，减少资源浪费
