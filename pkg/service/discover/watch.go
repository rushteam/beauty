package discover

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

var (
	watchRetryBase = 200 * time.Millisecond
	watchRetryMax  = 8 * time.Second
)

// WatchLoop 持续订阅 serviceName 的实例变化，阻塞直到 ctx 取消才返回。
//
// Discovery.Watch 的契约是阻塞到 ctx 取消，但后端断连、registry 被 Close、
// 初始化失败等情况下会提前返回。WatchLoop 此时按指数退避（上限 watchRetryMax）
// 重新订阅；单次 Watch 持续超过 watchRetryMax 视为曾正常工作，退避重置为初始值。
//
// 空推送保护：曾拿到过实例后再收到空列表时不下发，保留上次的地址。注册中心抖动、
// 重订阅后的首次推送等场景常出现瞬时空列表，下发会让客户端关掉全部连接；
// 真正缩容到 0 时旧地址只会连接失败，与无实例的结果相同。从未有过实例时空列表照常下发，便于快速失败。
func WatchLoop(ctx context.Context, d Discovery, serviceName string, n Notify) {
	n = protectEmpty(serviceName, n)
	backoff := watchRetryBase
	for ctx.Err() == nil {
		started := time.Now()
		err := d.Watch(ctx, serviceName, n)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > watchRetryMax {
			backoff = watchRetryBase
		}
		slog.Warn("discovery watch exited unexpectedly, reconnecting",
			slog.String("service", serviceName),
			slog.Any("err", err),
			slog.Duration("backoff", backoff))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, watchRetryMax)
	}
}

func protectEmpty(serviceName string, n Notify) Notify {
	var hadInstances atomic.Bool
	return func(services []ServiceInfo) {
		if len(services) == 0 && hadInstances.Load() {
			slog.Warn("discovery pushed empty instance list, keeping last known instances",
				slog.String("service", serviceName))
			return
		}
		if len(services) > 0 {
			hadInstances.Store(true)
		}
		n(services)
	}
}
