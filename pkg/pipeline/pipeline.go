// Package pipeline 提供类型安全的多阶段流水线:数据经过多个 Stage 顺序处理,
// 每个 Stage 可配置并发 worker 数(扇出)、带背压(有界 channel)。
//
// 典型场景:
//   - ETL 数据处理:读取 → 解析 → 转换 → 写入
//   - 图片处理:下载 → 解码 → 缩放 → 上传
//   - 日志处理:采集 → 过滤 → 聚合 → 存储
//
// 与 stream/router 区别:
//   - stream: 单阶段扇出/广播
//   - router: 按规则路由到不同目标
//   - pipeline: 多阶段顺序链,每阶段可并发,背压传导
//
// 纯标准库、泛型。
package pipeline

import (
	"context"
	"sync"
)

// ProcessFunc 阶段处理函数。输入一条数据,输出零或多条(通过 emit)。
// 返回 error 表示致命错误,将终止整个 pipeline。
type ProcessFunc[In, Out any] func(ctx context.Context, in In, emit func(Out)) error

// Stage 表示流水线的一个阶段配置。
type Stage[In, Out any] struct {
	Process ProcessFunc[In, Out]
	Workers int // 并发 worker 数,默认 1
	BufSize int // 输出 channel 缓冲大小,默认 0(同步)
}

// Pipe 连接两个阶段:从 input channel 读取,经 stage 处理,输出到新 channel。
// 返回输出 channel 和 error channel。当 input 关闭且所有 worker 完成后,输出 channel 关闭。
func Pipe[In, Out any](ctx context.Context, input <-chan In, stage Stage[In, Out]) (<-chan Out, <-chan error) {
	workers := stage.Workers
	if workers <= 0 {
		workers = 1
	}

	out := make(chan Out, stage.BufSize)
	errc := make(chan error, 1)

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-input:
					if !ok {
						return
					}
					err := stage.Process(ctx, item, func(o Out) {
						select {
						case out <- o:
						case <-ctx.Done():
						}
					})
					if err != nil {
						select {
						case errc <- err:
						default:
						}
						return
					}
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out, errc
}

// Run 执行完整的单阶段 pipeline:从 source 读,经 stage 处理,收集结果。
// 阻塞直到 source 关闭或 ctx 取消或出错。
func Run[In, Out any](ctx context.Context, source <-chan In, stage Stage[In, Out]) ([]Out, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out, errc := Pipe(ctx, source, stage)

	var results []Out
	for {
		select {
		case item, ok := <-out:
			if !ok {
				select {
				case err := <-errc:
					return results, err
				default:
					return results, nil
				}
			}
			results = append(results, item)
		case err := <-errc:
			cancel()
			// drain remaining
			for range out {
			}
			return results, err
		}
	}
}

// Source 将切片转为 channel(方便测试和简单场景)。
func Source[T any](ctx context.Context, items []T) <-chan T {
	ch := make(chan T, len(items))
	go func() {
		defer close(ch)
		for _, item := range items {
			select {
			case ch <- item:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// Merge 扇入:合并多个 channel 到一个。所有输入关闭后输出关闭。
func Merge[T any](ctx context.Context, channels ...<-chan T) <-chan T {
	out := make(chan T, len(channels))
	var wg sync.WaitGroup
	wg.Add(len(channels))

	for _, ch := range channels {
		go func(c <-chan T) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case v, ok := <-c:
					if !ok {
						return
					}
					select {
					case out <- v:
					case <-ctx.Done():
						return
					}
				}
			}
		}(ch)
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// Split 扇出:将一个 channel 复制到 n 个输出 channel。每条数据只发到一个输出(轮询分发)。
func Split[T any](ctx context.Context, input <-chan T, n int) []<-chan T {
	outs := make([]chan T, n)
	result := make([]<-chan T, n)
	for i := range outs {
		outs[i] = make(chan T, 1)
		result[i] = outs[i]
	}

	go func() {
		defer func() {
			for _, ch := range outs {
				close(ch)
			}
		}()
		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			case item, ok := <-input:
				if !ok {
					return
				}
				select {
				case outs[i%n] <- item:
				case <-ctx.Done():
					return
				}
				i++
			}
		}
	}()

	return result
}
