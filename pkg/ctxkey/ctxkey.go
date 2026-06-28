// Package ctxkey 提供类型安全的 context.Context 键。
//
// 问题:Go 的 context.Value 用 any 做 key,字符串 key 易冲突,各包自定
// `type contextKey struct{}` 又重复且零散(beauty 的 auth/requestid/callbacks/
// ratelimit/audit/afterwork/metadata 各自定义了一遍)。本包用泛型统一:
//
//	var userKey = ctxkey.New[auth.User]()
//	var reqKey  = ctxkey.New[string]()
//
// 类型参数 T 编译期约束存取类型,避免类型断言写错;每次 New 分配独立
// 内部指针,使同 T 的多个 Key 也互不冲突(解决了零大小结构体同类型=同 key 的问题)。
//
// 用法:在包级 var 声明时调一次 New,后续用该变量存取。不要每次存取都 New。
package ctxkey

import "context"

// Key 是一个类型安全的 context key。T 约束存取的值类型。
// 零值不可直接使用(需经 New 创建以获得独立标识);但 Key 是可比较的。
type Key[T any] struct {
	_ [0]int           // 零大小,不占内存
	id *uintptr        // 独立标识,New 时分配,使同 T 的不同 Key 区分
}

// New 创建一个独立的 Key。同 T 多次调用 New 返回的 Key 互不冲突
// (各自 id 指针不同)。在包级 var 声明时调用一次,后续复用该变量。
func New[T any]() Key[T] {
	var x uintptr
	return Key[T]{id: &x}
}

// With 把 value 关联到 key 并返回新 ctx。
func With[T any](ctx context.Context, key Key[T], value T) context.Context {
	return context.WithValue(ctx, key, value)
}

// Get 从 ctx 取出 key 关联的值。返回 (value, ok),ok=false 表示不存在或类型不符。
func Get[T any](ctx context.Context, key Key[T]) (T, bool) {
	v, ok := ctx.Value(key).(T)
	return v, ok
}

// MustGet 同 Get,但不存在时返回 T 零值(不 panic)。用于"可选注入"场景。
func MustGet[T any](ctx context.Context, key Key[T]) T {
	v, _ := ctx.Value(key).(T)
	return v
}
