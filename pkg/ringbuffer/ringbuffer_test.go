package ringbuffer_test

import (
	"reflect"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/ringbuffer"
)

func TestPushAndSlice_NotFull(t *testing.T) {
	r := ringbuffer.New[int](5)
	r.Push(1)
	r.Push(2)
	r.Push(3)
	if r.Len() != 3 || r.Full() {
		t.Fatalf("len=%d full=%v", r.Len(), r.Full())
	}
	if got := r.Slice(); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("slice = %v, want [1 2 3]", got)
	}
}

func TestOverwriteOldest(t *testing.T) {
	r := ringbuffer.New[int](3)
	for i := 1; i <= 5; i++ { // 1,2,3,4,5 → 只留最近 3 个:3,4,5
		r.Push(i)
	}
	if !r.Full() || r.Len() != 3 {
		t.Fatalf("len=%d full=%v", r.Len(), r.Full())
	}
	if got := r.Slice(); !reflect.DeepEqual(got, []int{3, 4, 5}) {
		t.Fatalf("slice = %v, want [3 4 5]", got)
	}
}

func TestRecent(t *testing.T) {
	r := ringbuffer.New[int](5)
	for i := 1; i <= 5; i++ {
		r.Push(i)
	}
	// 最近 3 条从新到旧:5,4,3
	if got := r.Recent(3); !reflect.DeepEqual(got, []int{5, 4, 3}) {
		t.Fatalf("recent(3) = %v, want [5 4 3]", got)
	}
	// n 超过元素数 → 全部,从新到旧
	if got := r.Recent(99); !reflect.DeepEqual(got, []int{5, 4, 3, 2, 1}) {
		t.Fatalf("recent(99) = %v", got)
	}
	if r.Recent(0) != nil {
		t.Fatal("recent(0) should be nil")
	}
}

func TestRecent_AfterWrap(t *testing.T) {
	r := ringbuffer.New[int](3)
	for i := 1; i <= 6; i++ { // 留 4,5,6
		r.Push(i)
	}
	if got := r.Recent(2); !reflect.DeepEqual(got, []int{6, 5}) {
		t.Fatalf("recent(2) after wrap = %v, want [6 5]", got)
	}
	if got := r.Slice(); !reflect.DeepEqual(got, []int{4, 5, 6}) {
		t.Fatalf("slice after wrap = %v, want [4 5 6]", got)
	}
}

func TestNewestOldest(t *testing.T) {
	r := ringbuffer.New[string](3)
	if _, ok := r.Newest(); ok {
		t.Fatal("empty Newest should be false")
	}
	if _, ok := r.Oldest(); ok {
		t.Fatal("empty Oldest should be false")
	}
	r.Push("a")
	r.Push("b")
	r.Push("c")
	r.Push("d") // 覆盖 a → b,c,d
	if v, ok := r.Newest(); !ok || v != "d" {
		t.Fatalf("newest = %q", v)
	}
	if v, ok := r.Oldest(); !ok || v != "b" {
		t.Fatalf("oldest = %q", v)
	}
}

func TestClear(t *testing.T) {
	r := ringbuffer.New[int](3)
	r.Push(1)
	r.Push(2)
	r.Clear()
	if r.Len() != 0 {
		t.Fatalf("len after clear = %d", r.Len())
	}
	if r.Slice() != nil && len(r.Slice()) != 0 {
		t.Fatalf("slice after clear = %v", r.Slice())
	}
	// 清空后仍可用
	r.Push(9)
	if v, _ := r.Newest(); v != 9 {
		t.Fatalf("reuse after clear: newest = %d", v)
	}
}

func TestCapClampedToOne(t *testing.T) {
	r := ringbuffer.New[int](0)
	if r.Cap() != 1 {
		t.Fatalf("cap = %d, want 1 (clamped)", r.Cap())
	}
	r.Push(1)
	r.Push(2)
	if got := r.Slice(); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("cap-1 ring = %v, want [2]", got)
	}
}

func TestSyncRing_Concurrent(t *testing.T) {
	s := ringbuffer.NewSync[int](100)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for i := range 1000 {
				s.Push(i)
			}
		})
	}
	for range 10 {
		wg.Go(func() {
			for range 1000 {
				_ = s.Recent(10)
				_ = s.Len()
			}
		})
	}
	wg.Wait()
	if s.Len() != 100 {
		t.Fatalf("len = %d, want 100 (full)", s.Len())
	}
}
