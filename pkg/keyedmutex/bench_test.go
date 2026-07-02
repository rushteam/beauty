package keyedmutex_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/keyedmutex"
)

// 无争用:每次 Lock/unlock 都建/回收 entry(测引用计数 + map 增删的开销)。
func BenchmarkLockUnlock_Uncontended(b *testing.B) {
	km := keyedmutex.New()
	b.ReportAllocs()
	for b.Loop() {
		unlock := km.Lock("k")
		unlock()
	}
}

// 多 key 并行、无跨 goroutine 争用:体现细粒度锁"不同 key 并行"的目的。
func BenchmarkLockUnlock_ParallelDistinctKeys(b *testing.B) {
	km := keyedmutex.New()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			unlock := km.Lock(string(rune('a' + (i & 15)))) // 16 个 key 轮转,分散争用
			unlock()
			i++
		}
	})
}

// 单一热点 key 并行:退化为一把普通互斥锁(争用上界)。
func BenchmarkLockUnlock_ParallelHotKey(b *testing.B) {
	km := keyedmutex.New()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			unlock := km.Lock("hot")
			unlock()
		}
	})
}
