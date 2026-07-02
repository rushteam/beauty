package idgen_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/idgen"
)

func BenchmarkNext(b *testing.B) {
	g, _ := idgen.New(1)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = g.Next()
	}
}

// 并发生成:验证单锁在多 goroutine 下的吞吐(会因序列号耗尽自旋而下降)。
func BenchmarkNextParallel(b *testing.B) {
	g, _ := idgen.New(1)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = g.Next()
		}
	})
}

func BenchmarkParse(b *testing.B) {
	g, _ := idgen.New(1)
	id := g.MustNext()
	b.ReportAllocs()
	for b.Loop() {
		_, _, _ = idgen.Parse(id)
	}
}
