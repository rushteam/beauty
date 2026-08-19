package syncx

import "golang.org/x/sync/singleflight"

// Group 对相同 key 的并发调用去重合并:同一时刻多个 goroutine Do 同一 key,只有一个真正执行
// fn,其余共享其结果——防缓存击穿/惊群。零值可用。V 是结果类型。
type Group[V any] struct {
	sf singleflight.Group
}

// Do 执行(或复用正在执行的)key 对应的 fn,返回结果、错误,以及本次结果是否被多个调用共享。
func (g *Group[V]) Do(key string, fn func() (V, error)) (V, error, bool) {
	v, err, shared := g.sf.Do(key, func() (any, error) { return fn() })
	res, _ := v.(V) // v 为 nil(如出错)时得零值,避免类型断言 panic
	return res, err, shared
}

// Forget 让 key 的下一次 Do 重新执行(不再复用在途结果)。
func (g *Group[V]) Forget(key string) { g.sf.Forget(key) }
