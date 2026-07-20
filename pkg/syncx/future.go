package syncx

import (
	"context"
	"fmt"
)

// Future 是一个异步计算的结果句柄。用 Async 发起,Await 取结果。
type Future[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// Async 在新 goroutine 里执行 fn,返回 Future;fn 内的 panic 会被捕获转成错误。
func Async[T any](fn func() (T, error)) *Future[T] {
	f := &Future[T]{done: make(chan struct{})}
	go func() {
		defer close(f.done)
		defer func() {
			if r := recover(); r != nil {
				f.err = fmt.Errorf("syncx: async panic: %v", r)
			}
		}()
		f.val, f.err = fn()
	}()
	return f
}

// Await 等待结果,或在 ctx 取消时返回 ctx 错误(此时后台计算仍会继续到自然结束)。
func (f *Future[T]) Await(ctx context.Context) (T, error) {
	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Done 返回完成信号 channel,便于在 select 中组合。
func (f *Future[T]) Done() <-chan struct{} { return f.done }
