// Package hedge 实现对冲请求(hedged / backup requests):一个操作发出后,若在 delay 内还没返回,
// 就并发补发一个副本(最多 maxHedge 个额外副本),取最先成功返回者,随后取消其余。
// 出自 Google《The Tail at Scale》——用少量额外请求换取尾延迟(P99/P999)的大幅下降,
// 适合读多、可重试、幂等的下游调用(RPC、存储、缓存回源)。
//
// 边界(机制而非策略):delay/maxHedge、哪些操作值得对冲、是否幂等都由调用方定;本包只负责
// "定时补发 + 取先成功 + 取消其余"。非幂等写操作请勿使用。纯标准库。
package hedge

import (
	"context"
	"time"
)

// Do 执行 fn 并按需对冲。primary(attempt=0)立即发出;此后每过 delay,若仍无成功结果且尚未达到
// 上限,就再补发一个副本(attempt 递增),最多 maxHedge 个额外副本(总计 maxHedge+1 个)。
// 返回最先成功(err==nil)的结果;若全部失败,返回最后一个错误。所有副本共享一个可取消的 ctx,
// 有结果或 Do 返回时其余副本会被取消——fn 应在收到 ctx.Done 时尽快返回。
//
// delay<=0 表示不等待、一次性并发发出全部副本(纯请求竞速)。maxHedge<=0 表示不对冲(只发一次)。
func Do[T any](ctx context.Context, delay time.Duration, maxHedge int, fn func(ctx context.Context, attempt int) (T, error)) (T, error) {
	if maxHedge < 0 {
		maxHedge = 0
	}
	total := maxHedge + 1

	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // 返回时取消所有在途副本

	type result struct {
		v   T
		err error
	}
	results := make(chan result, total) // 缓冲足够,副本永不因发送阻塞而泄漏

	launched := 0
	launch := func() {
		attempt := launched
		launched++
		go func() {
			v, err := fn(ctx, attempt)
			results <- result{v, err}
		}()
	}
	launch() // primary

	// arm 按需装填下一次补发的定时器;已达上限则不再补发。
	var timer *time.Timer
	var tick <-chan time.Time
	arm := func() {
		if launched >= total {
			tick = nil
			return
		}
		if delay <= 0 { // 不等待:一次性发满
			for launched < total {
				launch()
			}
			tick = nil
			return
		}
		if timer == nil {
			timer = time.NewTimer(delay)
		} else {
			timer.Reset(delay)
		}
		tick = timer.C
	}
	arm()
	if timer != nil {
		defer timer.Stop()
	}

	var last result
	completed := 0
	for {
		select {
		case <-ctx.Done():
			var zero T
			if last.err != nil {
				return zero, last.err
			}
			return zero, ctx.Err()
		case r := <-results:
			completed++
			if r.err == nil {
				return r.v, nil // 最先成功者胜出
			}
			last = r
			if completed >= total { // 全部副本都已失败
				var zero T
				return zero, r.err
			}
			// 还有副本在途或待补发:继续等待(补发由定时器驱动)。
		case <-tick:
			launch()
			arm()
		}
	}
}
