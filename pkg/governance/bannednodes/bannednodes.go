// Package bannednodes 提供单次请求内的节点禁用机制。
//
// 在客户端重试链中,失败节点会被加入本次请求的禁用列表,后续重试选节点时跳过,
// 避免一轮重试内重复打到同一故障节点。禁用列表通过 context 传递,只对本次请求生效,
// 不影响其他请求。与 pkg/governance/circuitbreaker 区别:熔断器是跨请求的持久状态,
// bannednodes 是单次请求内的瞬时状态(请求结束即丢弃)。
//
// 设计:用 map[string]struct{} 存禁用地址,节点数通常很少,O(1) 查询;
// 若调用方未注入列表,IsBanned 永远返回 false(零开销)。
package bannednodes

import (
	"context"
	"sync"

	"github.com/rushteam/beauty/pkg/ctxkey"
)

// bannedList 单次请求内的禁用节点集合。通过 ctx 传递,并发安全。
type bannedList struct {
	mu    sync.Mutex
	addrs map[string]struct{}
}

var key = ctxkey.New[*bannedList]()

// WithBannedNodes 在 ctx 注入一个空的禁用列表。调用方在重试链开始前调用一次,
// 后续 Ban 往里追加,selectService 用 IsBanned 过滤。未注入时 IsBanned 永远 false。
func WithBannedNodes(ctx context.Context) context.Context {
	return ctxkey.With(ctx, key, &bannedList{addrs: make(map[string]struct{})})
}

// IsInjected 判断 ctx 是否已注入禁用列表。供重试链入口决定是否自动注入(避免覆盖调用方已 Ban 的节点)。
func IsInjected(ctx context.Context) bool {
	_, ok := ctxkey.Get(ctx, key)
	return ok
}

// Ban 把 addrs 加入本次请求的禁用列表。若 ctx 未注入列表则静默忽略(零开销)。
func Ban(ctx context.Context, addrs ...string) {
	bl := ctxkey.MustGet(ctx, key)
	if bl == nil {
		return
	}
	bl.mu.Lock()
	defer bl.mu.Unlock()
	for _, a := range addrs {
		bl.addrs[a] = struct{}{}
	}
}

// IsBanned 判断 addr 是否在本次请求的禁用列表中。未注入列表时返回 false。
func IsBanned(ctx context.Context, addr string) bool {
	bl := ctxkey.MustGet(ctx, key)
	if bl == nil {
		return false
	}
	bl.mu.Lock()
	defer bl.mu.Unlock()
	_, banned := bl.addrs[addr]
	return banned
}

// BannedAddrs 返回本次请求已禁用的地址快照(调试/日志用)。未注入列表时返回 nil。
func BannedAddrs(ctx context.Context) []string {
	bl := ctxkey.MustGet(ctx, key)
	if bl == nil {
		return nil
	}
	bl.mu.Lock()
	defer bl.mu.Unlock()
	out := make([]string, 0, len(bl.addrs))
	for a := range bl.addrs {
		out = append(out, a)
	}
	return out
}
