package priority

import (
	"sort"
	"sync"
	"testing"
)

func intLess(a, b int) bool { return a < b }

func TestQueue_PushPop(t *testing.T) {
	q := New[int](intLess)
	q.Push(5)
	q.Push(1)
	q.Push(3)
	q.Push(2)
	q.Push(4)

	expected := []int{1, 2, 3, 4, 5}
	for i, want := range expected {
		got := q.Pop()
		if got != want {
			t.Fatalf("Pop[%d] = %d, want %d", i, got, want)
		}
	}
}

func TestQueue_MaxHeap(t *testing.T) {
	q := New[int](func(a, b int) bool { return a > b })
	q.Push(5)
	q.Push(1)
	q.Push(3)

	if got := q.Pop(); got != 5 {
		t.Fatalf("max Pop = %d, want 5", got)
	}
	if got := q.Pop(); got != 3 {
		t.Fatalf("max Pop = %d, want 3", got)
	}
}

func TestQueue_Peek(t *testing.T) {
	q := New[int](intLess)
	q.Push(3)
	q.Push(1)
	q.Push(2)

	if got := q.Peek(); got != 1 {
		t.Fatalf("Peek = %d, want 1", got)
	}
	if q.Len() != 3 {
		t.Fatalf("Len after Peek = %d, want 3", q.Len())
	}
}

func TestQueue_PushPop_Method(t *testing.T) {
	q := New[int](intLess)
	q.Push(3)
	q.Push(5)

	// PushPop(1): 1 < 3(top), so pop 1 back immediately
	got := q.PushPop(1)
	if got != 1 {
		t.Fatalf("PushPop(1) = %d, want 1", got)
	}

	// PushPop(10): 3(top) < 10, so swap and return 3
	got = q.PushPop(10)
	if got != 3 {
		t.Fatalf("PushPop(10) = %d, want 3", got)
	}
}

func TestQueue_Remove(t *testing.T) {
	q := New[int](intLess)
	q.Push(5)
	q.Push(1)
	q.Push(3)
	q.Push(2)
	q.Push(4)

	// Remove element at index 0 (the min, which is 1)
	removed := q.Remove(0)
	if removed != 1 {
		t.Fatalf("Remove(0) = %d, want 1", removed)
	}
	if q.Len() != 4 {
		t.Fatalf("Len = %d, want 4", q.Len())
	}

	// Remaining should still be a valid heap
	var result []int
	for q.Len() > 0 {
		result = append(result, q.Pop())
	}
	if !sort.IntsAreSorted(result) {
		t.Fatalf("after Remove, pop order not sorted: %v", result)
	}
}

func TestQueue_Update(t *testing.T) {
	type item struct {
		name     string
		priority int
	}
	less := func(a, b item) bool { return a.priority < b.priority }
	q := New[item](less)

	q.Push(item{"a", 3})
	q.Push(item{"b", 1})
	q.Push(item{"c", 2})

	// "a" is at some index, change its priority
	// Find "a" first
	idx := -1
	for i := 0; i < q.Len(); i++ {
		if q.At(i).name == "a" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("item 'a' not found")
	}

	// Change "a" priority to 0 (highest)
	q.items[idx] = item{"a", 0}
	q.Update(idx)

	got := q.Pop()
	if got.name != "a" {
		t.Fatalf("after Update, Pop = %v, want 'a'", got)
	}
}

func TestQueue_Items(t *testing.T) {
	q := New[int](intLess)
	q.Push(3)
	q.Push(1)
	q.Push(2)

	items := q.Items()
	if len(items) != 3 {
		t.Fatalf("Items len = %d, want 3", len(items))
	}
	// Verify it's a copy
	items[0] = 999
	if q.Peek() == 999 {
		t.Fatal("Items returned reference not copy")
	}
}

func TestQueue_Empty(t *testing.T) {
	q := New[int](intLess)
	if q.Len() != 0 {
		t.Fatalf("empty Len = %d", q.Len())
	}
}

func TestQueue_SingleElement(t *testing.T) {
	q := New[int](intLess)
	q.Push(42)
	if q.Peek() != 42 {
		t.Fatalf("Peek = %d, want 42", q.Peek())
	}
	if q.Pop() != 42 {
		t.Fatal("Pop != 42")
	}
	if q.Len() != 0 {
		t.Fatal("not empty after Pop")
	}
}

func TestQueue_LargeRandom(t *testing.T) {
	q := New[int](intLess)
	vals := []int{89, 23, 56, 12, 90, 1, 45, 78, 34, 67, 100, 5, 50, 28, 73, 15, 62, 38, 95, 8}
	for _, v := range vals {
		q.Push(v)
	}

	var result []int
	for q.Len() > 0 {
		result = append(result, q.Pop())
	}
	if !sort.IntsAreSorted(result) {
		t.Fatalf("not sorted: %v", result)
	}
}

func TestSyncQueue_PushPop(t *testing.T) {
	sq := NewSync[int](intLess)
	sq.Push(5)
	sq.Push(1)
	sq.Push(3)

	got, ok := sq.Pop()
	if !ok || got != 1 {
		t.Fatalf("Pop = (%d, %v), want (1, true)", got, ok)
	}
}

func TestSyncQueue_PopEmpty(t *testing.T) {
	sq := NewSync[int](intLess)
	_, ok := sq.Pop()
	if ok {
		t.Fatal("Pop on empty returned ok=true")
	}
}

func TestSyncQueue_PeekEmpty(t *testing.T) {
	sq := NewSync[int](intLess)
	_, ok := sq.Peek()
	if ok {
		t.Fatal("Peek on empty returned ok=true")
	}
}

func TestSyncQueue_Concurrent(t *testing.T) {
	sq := NewSync[int](intLess)
	var wg sync.WaitGroup

	// Push concurrently
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sq.Push(n)
		}(i)
	}
	wg.Wait()

	if sq.Len() != 100 {
		t.Fatalf("Len = %d, want 100", sq.Len())
	}

	// Pop concurrently
	var popped sync.Map
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, ok := sq.Pop()
			if ok {
				popped.Store(v, true)
			}
		}()
	}
	wg.Wait()

	if sq.Len() != 0 {
		t.Fatalf("Len after concurrent Pop = %d, want 0", sq.Len())
	}

	// Verify all 100 items popped
	count := 0
	popped.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 100 {
		t.Fatalf("popped count = %d, want 100", count)
	}
}

func TestSyncQueue_PushPop_Method(t *testing.T) {
	sq := NewSync[int](intLess)
	sq.Push(5)
	sq.Push(3)

	got := sq.PushPop(1)
	if got != 1 {
		t.Fatalf("PushPop(1) = %d, want 1", got)
	}

	got = sq.PushPop(10)
	if got != 3 {
		t.Fatalf("PushPop(10) = %d, want 3", got)
	}
}
