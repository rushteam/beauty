package counter_test

import (
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/counter"
)

func BenchmarkIncr(b *testing.B) {
	c := counter.New(time.Minute)
	defer c.Stop()
	b.ReportAllocs()
	for b.Loop() {
		c.Incr("k", 1)
	}
}

func BenchmarkAllow(b *testing.B) {
	c := counter.New(time.Minute)
	defer c.Stop()
	b.ReportAllocs()
	for b.Loop() {
		c.Allow("k", 1, 1<<62) // 配额极大,恒放行,测纯开销
	}
}

// 单一热点 key:所有 goroutine 争同一分片锁(最坏情况)。
func BenchmarkIncrParallelHotKey(b *testing.B) {
	c := counter.New(time.Minute)
	defer c.Stop()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Incr("hot", 1)
		}
	})
}

// 多 key:分片锁分散争用,应显著优于热点 key。
func BenchmarkIncrParallelManyKeys(b *testing.B) {
	c := counter.New(time.Minute)
	defer c.Stop()
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Incr(keys[i%len(keys)], 1)
			i++
		}
	})
}
