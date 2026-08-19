// Package syncx 提供一组便捷的并发原语(泛型),补齐标准库与 pkg/xgo 之外常被手搓、
// 又容易写错的模式:
//   - Map / ForEach:对一批元素并发处理,带并发上限 + 错误聚合(首个出错即取消其余);
//   - SingleFlight:并发相同 key 的调用去重合并,防缓存击穿/惊群;
//   - Batcher:攒够 N 条或到时间就 flush,批量写库/推送/调用;
//   - Debounce / Throttle:事件去抖 / 限频;
//   - Future / Async:异步跑一个函数,稍后 Await 取结果。
//
// 仅依赖标准库与 golang.org/x/sync。
package syncx

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// Map 对 items 并发执行 fn,最多 limit 个并发(limit<=0 表示不限),按输入顺序返回结果。
// 任一 fn 出错则取消其余(通过传入 fn 的 ctx)并返回首个错误。
func Map[T, R any](ctx context.Context, items []T, limit int, fn func(context.Context, T) (R, error)) ([]R, error) {
	results := make([]R, len(items))
	g, ctx := errgroup.WithContext(ctx)
	if limit > 0 {
		g.SetLimit(limit)
	}
	for i, it := range items {
		g.Go(func() error {
			r, err := fn(ctx, it)
			if err != nil {
				return err
			}
			results[i] = r // 各 goroutine 写不同下标,无需加锁
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// ForEach 对 items 并发执行 fn(不收集结果),最多 limit 个并发(limit<=0 不限)。
// 任一出错取消其余并返回首个错误。
func ForEach[T any](ctx context.Context, items []T, limit int, fn func(context.Context, T) error) error {
	g, ctx := errgroup.WithContext(ctx)
	if limit > 0 {
		g.SetLimit(limit)
	}
	for _, it := range items {
		g.Go(func() error { return fn(ctx, it) })
	}
	return g.Wait()
}
