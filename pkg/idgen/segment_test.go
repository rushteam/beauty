package idgen

import (
	"context"
	"sync"
	"testing"
)

func TestSegmentMonotonicUnique(t *testing.T) {
	s := NewSegment(NewMemorySegmentStore(10), "order")
	ctx := context.Background()
	seen := make(map[int64]bool)
	prev := int64(-1)
	for i := 0; i < 1000; i++ {
		id, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		if id <= prev {
			t.Fatalf("not monotonic: %d after %d", id, prev)
		}
		seen[id] = true
		prev = id
	}
}

func TestSegmentConcurrentUnique(t *testing.T) {
	s := NewSegment(NewMemorySegmentStore(100), "x")
	ctx := context.Background()
	const g, n = 16, 2000
	var mu sync.Mutex
	seen := make(map[int64]struct{}, g*n)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]int64, 0, n)
			for j := 0; j < n; j++ {
				id, err := s.Next(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				local = append(local, id)
			}
			mu.Lock()
			for _, id := range local {
				if _, dup := seen[id]; dup {
					t.Errorf("duplicate id %d", id)
				}
				seen[id] = struct{}{}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != g*n {
		t.Fatalf("unique count=%d want %d", len(seen), g*n)
	}
}

// 验证跨号段边界连续(step 小、取用多,必然多次批发)。
func TestSegmentCrossBoundary(t *testing.T) {
	s := NewSegment(NewMemorySegmentStore(7), "t", WithPreloadThreshold(0.5))
	ctx := context.Background()
	for i := int64(0); i < 100; i++ {
		id, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if id != i {
			t.Fatalf("id=%d want %d (memory store starts at 0, contiguous)", id, i)
		}
	}
}
