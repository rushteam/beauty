package syncx

import (
	"sync"
	"time"
)

// Debounce 返回一个去抖函数 call:每次调用都会把 fn 的执行推迟 d;只有在 d 内不再被调用时 fn 才触发
// (即"最后一次调用后静默 d 才执行")。cancel 取消尚未触发的待执行。适合配置热更防抖、搜索联想等。
// fn 在独立的 time.AfterFunc goroutine 里执行。
func Debounce(d time.Duration, fn func()) (call func(), cancel func()) {
	var (
		mu sync.Mutex
		t  *time.Timer
	)
	call = func() {
		mu.Lock()
		defer mu.Unlock()
		if t != nil {
			t.Stop()
		}
		t = time.AfterFunc(d, fn)
	}
	cancel = func() {
		mu.Lock()
		defer mu.Unlock()
		if t != nil {
			t.Stop()
			t = nil
		}
	}
	return call, cancel
}

// Throttle 返回一个限频函数:每 d 内最多触发一次 fn(前沿触发——窗口内首次调用立即执行,
// 其余忽略)。适合按钮防连点、高频事件降频。fn 同步执行。
func Throttle(d time.Duration, fn func()) func() {
	var (
		mu   sync.Mutex
		last time.Time
	)
	return func() {
		mu.Lock()
		now := time.Now()
		ok := last.IsZero() || now.Sub(last) >= d
		if ok {
			last = now
		}
		mu.Unlock()
		if ok {
			fn()
		}
	}
}
