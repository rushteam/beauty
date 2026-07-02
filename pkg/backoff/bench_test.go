package backoff_test

import (
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
)

func BenchmarkDuration_Full(b *testing.B) {
	p := backoff.New(backoff.WithJitter(backoff.JitterFull))
	b.ReportAllocs()
	for b.Loop() {
		_ = p.Duration(5)
	}
}

func BenchmarkDuration_None(b *testing.B) {
	p := backoff.New(backoff.WithJitter(backoff.JitterNone))
	b.ReportAllocs()
	for b.Loop() {
		_ = p.Duration(5)
	}
}

func BenchmarkDuration_Proportional(b *testing.B) {
	p := backoff.New(backoff.WithJitter(backoff.JitterProportional), backoff.WithJitterRatio(0.25))
	b.ReportAllocs()
	for b.Loop() {
		_ = p.Duration(5)
	}
}

func BenchmarkDuration_Parallel(b *testing.B) {
	p := backoff.New(backoff.WithBase(time.Second))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = p.Duration(3)
		}
	})
}
