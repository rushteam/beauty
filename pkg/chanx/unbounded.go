// Package chanx 提供无界 channel 等 channel 扩展原语。
package chanx

// Unbounded 是一个无界 channel：发送端永不阻塞（值缓存在内部切片中），
// 接收端从 Out() 读取，保持 FIFO 顺序。适合事件总线、日志队列等
// "生产不可阻塞、消费可能滞后" 的场景。
//
// 使用：
//
//	u := chanx.NewUnbounded[int]()
//	u.In() <- 1
//	v := <-u.Out()
//	u.Close() // 关闭后 Out() 读完剩余值再关闭
type Unbounded[T any] struct {
	in  chan T
	out chan T
}

// NewUnbounded 创建并启动一个无界 channel。
func NewUnbounded[T any]() *Unbounded[T] {
	u := &Unbounded[T]{
		in:  make(chan T),
		out: make(chan T),
	}
	go u.run()
	return u
}

// In 返回发送端（永不阻塞）。Close 后不可再发送。
func (u *Unbounded[T]) In() chan<- T { return u.in }

// Out 返回接收端。In 关闭且缓冲读尽后，Out 也会关闭。
func (u *Unbounded[T]) Out() <-chan T { return u.out }

// Close 关闭发送端；缓冲中剩余的值仍可从 Out 读出，读完后 Out 关闭。
func (u *Unbounded[T]) Close() { close(u.in) }

func (u *Unbounded[T]) run() {
	defer close(u.out)
	var buf []T
	for {
		if len(buf) == 0 {
			v, ok := <-u.in
			if !ok {
				return
			}
			buf = append(buf, v)
		}
		select {
		case v, ok := <-u.in:
			if !ok {
				// 发送端已关闭：把剩余缓冲全部送出后退出
				for _, x := range buf {
					u.out <- x
				}
				return
			}
			buf = append(buf, v)
		case u.out <- buf[0]:
			buf[0] = *new(T) // 释放引用，便于 GC
			buf = buf[1:]
		}
	}
}
