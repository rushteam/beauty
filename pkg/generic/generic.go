// Package generic 提供一组类型安全的泛型/反射小工具。
// 仅收录标准库 slices/maps 未覆盖的部分。
package generic

import (
	"reflect"
	"regexp"
	"runtime"
	"strings"
)

// Ptr 返回 v 的指针，便于给需要 *T 的字段赋字面量。
//
//	cfg.Timeout = generic.Ptr(30 * time.Second)
func Ptr[T any](v T) *T {
	return &v
}

// New 创建 T 的可用零值实例：对 map/slice 会 make，对（多级）指针会逐层分配，
// 因此 New[*Foo]() 返回的是指向零值 Foo 的非 nil 指针，而非 nil。
// （需要纯类型时用标准库 reflect.TypeFor[T]() 即可，本包不再重复。）
func New[T any]() T {
	typ := reflect.TypeFor[T]()
	switch typ.Kind() {
	case reflect.Map:
		return reflect.MakeMap(typ).Interface().(T)
	case reflect.Slice:
		return reflect.MakeSlice(typ, 0, 0).Interface().(T)
	case reflect.Pointer:
		origin := reflect.New(typ.Elem())
		inst := origin
		for et := typ.Elem(); et.Kind() == reflect.Pointer; et = et.Elem() {
			inst.Elem().Set(reflect.New(et.Elem()))
			inst = inst.Elem()
		}
		return origin.Interface().(T)
	default:
		var t T
		return t
	}
}

// ToMap 用 f 把切片转换为 map，常用于按某个字段建索引。
//
//	byID := generic.ToMap(users, func(u User) (int, User) { return u.ID, u })
func ToMap[T any, K comparable, V any](s []T, f func(T) (K, V)) map[K]V {
	m := make(map[K]V, len(s))
	for _, e := range s {
		k, v := f(e)
		m[k] = v
	}
	return m
}

var (
	regAnonymousFunc = regexp.MustCompile(`^func[0-9]+`)
	regNumber        = regexp.MustCompile(`^\d+$`)
)

// TypeName 返回 reflect.Value 的人类可读类型名，便于日志/调试：
//   - 多级指针自动解引用（**Foo -> "Foo"）
//   - 函数取其名（method、匿名函数等），匿名函数返回空串
func TypeName(val reflect.Value) string {
	typ := val.Type()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Func {
		full := runtime.FuncForPC(val.Pointer()).Name()
		idx := strings.LastIndex(full, ".")
		if idx < 0 {
			return full
		}
		name := full[idx+1:]
		if regAnonymousFunc.MatchString(name) || regNumber.MatchString(name) {
			return ""
		}
		return name
	}
	return typ.Name()
}
