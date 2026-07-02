package keyedmutex_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/keyedmutex"
)

func TestSameKeySerializes(t *testing.T) {
	km := keyedmutex.New()
	var inCritical atomic.Int32
	var maxConcurrent atomic.Int32

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			unlock := km.Lock("k")
			defer unlock()
			n := inCritical.Add(1)
			if n > maxConcurrent.Load() {
				maxConcurrent.Store(n)
			}
			time.Sleep(time.Millisecond)
			inCritical.Add(-1)
		})
	}
	wg.Wait()
	if maxConcurrent.Load() != 1 {
		t.Fatalf("same key should serialize, max concurrent = %d", maxConcurrent.Load())
	}
}

func TestDifferentKeysParallel(t *testing.T) {
	km := keyedmutex.New()
	const n = 20
	var running atomic.Int32
	var maxParallel atomic.Int32
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i := range n {
		key := string(rune('a' + i))
		wg.Go(func() {
			<-start
			unlock := km.Lock(key)
			defer unlock()
			cur := running.Add(1)
			for {
				old := maxParallel.Load()
				if cur <= old || maxParallel.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
		})
	}
	close(start)
	wg.Wait()
	// 不同 key 应能并行(不要求全部,但显著 >1)。
	if maxParallel.Load() < 2 {
		t.Fatalf("different keys should run in parallel, max = %d", maxParallel.Load())
	}
}

func TestMutualExclusionCorrectness(t *testing.T) {
	km := keyedmutex.New()
	counter := 0 // 非原子:靠 keyedmutex 保护
	const goroutines, per = 40, 500

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range per {
				unlock := km.Lock("shared")
				counter++ // 临界区
				unlock()
			}
		})
	}
	wg.Wait()
	if counter != goroutines*per {
		t.Fatalf("counter = %d, want %d (lost updates = race)", counter, goroutines*per)
	}
}

func TestTryLock(t *testing.T) {
	km := keyedmutex.New()
	unlock, ok := km.TryLock("k")
	if !ok {
		t.Fatal("first TryLock should succeed")
	}
	if _, ok2 := km.TryLock("k"); ok2 {
		t.Fatal("second TryLock on held key should fail")
	}
	// 其他 key 不受影响
	if u2, ok3 := km.TryLock("other"); !ok3 {
		t.Fatal("TryLock on different key should succeed")
	} else {
		u2()
	}
	unlock()
	// 释放后可再获取
	if u3, ok4 := km.TryLock("k"); !ok4 {
		t.Fatal("TryLock after unlock should succeed")
	} else {
		u3()
	}
}

func TestDo(t *testing.T) {
	km := keyedmutex.New()
	var n atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			km.Do("k", func() { n.Add(1) })
		})
	}
	wg.Wait()
	if n.Load() != 100 {
		t.Fatalf("Do ran %d times, want 100", n.Load())
	}
}

func TestNoLeak_LenReturnsToZero(t *testing.T) {
	km := keyedmutex.New()
	var wg sync.WaitGroup
	for i := range 1000 {
		key := string(rune('a'+i%26)) + string(rune(i))
		wg.Go(func() {
			unlock := km.Lock(key)
			unlock()
		})
	}
	wg.Wait()
	if got := km.Len(); got != 0 {
		t.Fatalf("entries should be reclaimed, Len = %d", got)
	}
}

func TestUnlockIdempotent(t *testing.T) {
	km := keyedmutex.New()
	unlock := km.Lock("k")
	unlock()
	unlock() // 第二次应无副作用(sync.Once)
	if km.Len() != 0 {
		t.Fatalf("double unlock corrupted state, Len = %d", km.Len())
	}
	// 状态未被破坏,能正常再用
	u2 := km.Lock("k")
	u2()
}
