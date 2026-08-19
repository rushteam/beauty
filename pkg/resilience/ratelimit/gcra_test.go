package ratelimit

import (
	"testing"
	"time"
)

func TestGCRA_BurstThenThrottle(t *testing.T) {
	// 10/s(emission=100ms),burst=3:同一瞬间应放行 3 次,第 4 次被拒。
	g := NewGCRA(10, 3)
	defer g.Stop()

	for i := 0; i < 3; i++ {
		if ok, _ := g.Allow("k"); !ok {
			t.Fatalf("第 %d 次应放行(burst=3)", i+1)
		}
	}
	ok, retry := g.Allow("k")
	if ok {
		t.Fatal("第 4 次应被拒")
	}
	if retry <= 0 || retry > 100*time.Millisecond {
		t.Fatalf("retryAfter 应约等于 emission(<=100ms),got %s", retry)
	}
}

func TestGCRA_RefillOverTime(t *testing.T) {
	g := NewGCRA(50, 1) // emission=20ms,burst=1
	defer g.Stop()

	if ok, _ := g.Allow("k"); !ok {
		t.Fatal("首次应放行")
	}
	if ok, _ := g.Allow("k"); ok {
		t.Fatal("紧接着应被拒(burst=1)")
	}
	time.Sleep(25 * time.Millisecond) // 等一个 emission
	if ok, _ := g.Allow("k"); !ok {
		t.Fatal("等待 emission 后应再次放行")
	}
}

func TestGCRA_PerKeyIsolation(t *testing.T) {
	g := NewGCRA(1, 1)
	defer g.Stop()
	if ok, _ := g.Allow("a"); !ok {
		t.Fatal("a 首次应放行")
	}
	if ok, _ := g.Allow("b"); !ok {
		t.Fatal("b 应独立放行,不受 a 影响")
	}
}

func TestGCRA_Unlimited(t *testing.T) {
	g := NewGCRA(0, 0) // 不限
	defer g.Stop()
	for i := 0; i < 100; i++ {
		if ok, _ := g.Allow("k"); !ok {
			t.Fatal("rate<=0 应恒放行")
		}
	}
}

// 编译期断言:GCRA 实现 Limiter,可直接接 Middleware。
var _ Limiter = (*GCRA)(nil)
