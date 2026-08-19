// Package options 提供分层的功能选项（functional options）模式：
// 框架定义一组“通用选项”，各实现方再追加自己的“专属选项”，调用方把两类选项
// 混在同一个 []Option 里传入；框架与实现各自用类型安全的方式提取属于自己的部分。
//
// 好处：实现方无需 import 框架的选项类型即可扩展选项，消除反向依赖；
// 提取通过泛型类型断言完成，避免 interface{} 误用。
//
// 用法概览：
//
//	// —— 框架侧：定义通用选项 ——
//	type Options struct{ Timeout time.Duration }
//	func WithTimeout(d time.Duration) options.Option {
//	    return options.Common(func(o *Options) { o.Timeout = d })
//	}
//
//	// —— 实现方侧：定义专属选项（不依赖框架选项类型）——
//	type RedisOptions struct{ PoolSize int }
//	func WithPoolSize(n int) options.Option {
//	    return options.Impl(func(o *RedisOptions) { o.PoolSize = n })
//	}
//
//	// —— 实现方在构造时提取两类选项 ——
//	func New(opts ...options.Option) *Redis {
//	    common := options.ApplyCommon(&Options{Timeout: time.Second}, opts...)
//	    own    := options.ApplyImpl(&RedisOptions{PoolSize: 10}, opts...)
//	    ...
//	}
//
//	// —— 调用方混用 ——
//	New(WithTimeout(3*time.Second), WithPoolSize(50))
package options

// Option 是一个选项载体，内部携带“通用”或“专属”两类修改函数之一。
type Option struct {
	common any // func(*O)：作用于框架的通用选项结构
	impl   any // func(*T)：作用于某实现方的专属选项结构
}

// Common 构造一个作用于通用选项结构 O 的选项。
func Common[O any](fn func(*O)) Option {
	return Option{common: fn}
}

// Impl 构造一个作用于实现方专属选项结构 T 的选项。
func Impl[T any](fn func(*T)) Option {
	return Option{impl: fn}
}

// ApplyCommon 把 opts 中所有作用于 *O 的通用选项应用到 base 并返回。
// 非 *O 的选项（其它实现方的专属选项）被自动忽略。
func ApplyCommon[O any](base *O, opts ...Option) *O {
	for _, o := range opts {
		if fn, ok := o.common.(func(*O)); ok {
			fn(base)
		}
	}
	return base
}

// ApplyImpl 把 opts 中所有作用于 *T 的专属选项应用到 base 并返回。
// 类型不匹配的专属选项（属于别的实现）被自动忽略。
func ApplyImpl[T any](base *T, opts ...Option) *T {
	for _, o := range opts {
		if fn, ok := o.impl.(func(*T)); ok {
			fn(base)
		}
	}
	return base
}
