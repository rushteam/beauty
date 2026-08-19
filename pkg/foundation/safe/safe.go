// Package safe 提供 panic 恢复相关的小工具：把 panic 转成带堆栈的 error，
// 以及安全启动 goroutine（panic 不会崩进程）。
package safe

import (
	"fmt"
	"runtime/debug"
)

// PanicError 包装一个被 recover 的 panic 值及其堆栈。
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v\n%s", e.Value, e.Stack)
}

// Unwrap 在 panic 值本身是 error 时暴露它，便于 errors.Is/As。
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}

// Run 执行 fn，并把其中的 panic 转换为 *PanicError 返回。
func Run(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	return fn()
}

// Go 在新 goroutine 中执行 fn，recover panic 并交给 onPanic（可为 nil），
// 避免一个后台 goroutine 的 panic 拖垮整个进程。
func Go(fn func(), onPanic func(error)) {
	go func() {
		defer func() {
			if r := recover(); r != nil && onPanic != nil {
				onPanic(&PanicError{Value: r, Stack: debug.Stack()})
			}
		}()
		fn()
	}()
}
