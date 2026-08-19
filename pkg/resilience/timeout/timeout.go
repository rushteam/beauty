// Package timeout 提供统一的超时执行包装:给任意函数加超时 + panic 恢复 + 错误分类。
//
// 比裸 context.WithTimeout 多:
//   - panic 自动恢复(不会打崩调用方 goroutine)
//   - 超时/panic/业务错误 三类错误明确区分(errors.Is 可判)
//   - 结果泛型返回(不只 error)
//
// 典型场景:
//   - 调用第三方 SDK(无 context 感知,可能卡死)
//   - 执行用户插件/脚本(不信任代码,需超时 + panic 保护)
//   - 统一 RPC 超时包装(日志/metric 统一埋点)
//
// 纯标准库、并发安全(无状态)。
package timeout

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrTimeout 表示执行超时。可用 errors.Is 判定。
var ErrTimeout = errors.New("timeout: deadline exceeded")

// PanicError 表示 fn 内发生了 panic。
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("timeout: panic: %v", e.Value)
}

// Is 支持 errors.Is(err, ErrPanic) 判定。
func (e *PanicError) Is(target error) bool {
	_, ok := target.(*PanicError)
	return ok
}

// ErrPanic 用于 errors.Is 判定 panic 类错误的哨兵值。
var ErrPanic = &PanicError{}

// Do 在 timeout 内执行 fn。超时返回 ErrTimeout;fn panic 返回 *PanicError;
// 正常则返回 fn 的 error。
//
// fn 运行在独立 goroutine 中;超时后 fn 仍在运行(Go 无法强杀 goroutine),
// 但调用方立即返回。如果需要 fn 配合退出,应在 fn 内检查 ctx。
func Do(ctx context.Context, d time.Duration, fn func(ctx context.Context) error) error {
	_, err := DoValue(ctx, d, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// DoValue 泛型版:超时内执行 fn 并返回结果 T。语义同 Do。
func DoValue[T any](ctx context.Context, d time.Duration, fn func(ctx context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	type result struct {
		val T
		err error
	}

	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				var zero T
				ch <- result{zero, &PanicError{Value: r}}
			}
		}()
		v, err := fn(ctx)
		ch <- result{v, err}
	}()

	select {
	case res := <-ch:
		return res.val, res.err
	case <-ctx.Done():
		var zero T
		if ctx.Err() == context.DeadlineExceeded {
			return zero, ErrTimeout
		}
		return zero, ctx.Err()
	}
}
