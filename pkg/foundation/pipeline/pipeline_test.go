package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestPipe_Basic(t *testing.T) {
	ctx := context.Background()
	src := Source(ctx, []int{1, 2, 3, 4, 5})

	double := Stage[int, int]{
		Process: func(ctx context.Context, in int, emit func(int)) error {
			emit(in * 2)
			return nil
		},
	}

	results, err := Run(ctx, src, double)
	if err != nil {
		t.Fatal(err)
	}
	sort.Ints(results)
	expected := []int{2, 4, 6, 8, 10}
	if len(results) != len(expected) {
		t.Fatalf("results = %v, want %v", results, expected)
	}
	for i, v := range expected {
		if results[i] != v {
			t.Fatalf("results[%d] = %d, want %d", i, results[i], v)
		}
	}
}

func TestPipe_MultiWorker(t *testing.T) {
	ctx := context.Background()
	src := Source(ctx, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})

	stage := Stage[int, int]{
		Process: func(ctx context.Context, in int, emit func(int)) error {
			emit(in * 10)
			return nil
		},
		Workers: 4,
		BufSize: 5,
	}

	results, err := Run(ctx, src, stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 10 {
		t.Fatalf("len = %d, want 10", len(results))
	}
	sort.Ints(results)
	for i, v := range results {
		if v != (i+1)*10 {
			t.Fatalf("results[%d] = %d, want %d", i, v, (i+1)*10)
		}
	}
}

func TestPipe_FilterAndExpand(t *testing.T) {
	ctx := context.Background()
	src := Source(ctx, []int{1, 2, 3, 4, 5})

	// Filter: only even, and duplicate them
	stage := Stage[int, int]{
		Process: func(ctx context.Context, in int, emit func(int)) error {
			if in%2 == 0 {
				emit(in)
				emit(in) // emit twice
			}
			return nil
		},
	}

	results, err := Run(ctx, src, stage)
	if err != nil {
		t.Fatal(err)
	}
	sort.Ints(results)
	expected := []int{2, 2, 4, 4}
	if len(results) != len(expected) {
		t.Fatalf("results = %v, want %v", results, expected)
	}
}

func TestPipe_Error(t *testing.T) {
	ctx := context.Background()
	src := Source(ctx, []int{1, 2, 3, 4, 5})

	boom := errors.New("boom")
	stage := Stage[int, int]{
		Process: func(ctx context.Context, in int, emit func(int)) error {
			if in == 3 {
				return boom
			}
			emit(in)
			return nil
		},
		Workers: 1,
	}

	_, err := Run(ctx, src, stage)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
}

func TestPipe_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	src := make(chan int)
	go func() {
		defer close(src)
		for i := 0; ; i++ {
			select {
			case src <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	stage := Stage[int, int]{
		Process: func(ctx context.Context, in int, emit func(int)) error {
			emit(in)
			return nil
		},
		Workers: 2,
	}

	out, _ := Pipe(ctx, src, stage)

	// Read a few then cancel
	<-out
	<-out
	cancel()

	// Should drain quickly
	time.Sleep(50 * time.Millisecond)
}

func TestPipe_Chain(t *testing.T) {
	ctx := context.Background()
	src := Source(ctx, []string{"hello", "world", "go"})

	// Stage 1: string → int (length)
	s1 := Stage[string, int]{
		Process: func(ctx context.Context, in string, emit func(int)) error {
			emit(len(in))
			return nil
		},
		Workers: 2,
	}

	// Stage 2: int → string (format)
	s2 := Stage[int, string]{
		Process: func(ctx context.Context, in int, emit func(string)) error {
			emit(fmt.Sprintf("len=%d", in))
			return nil
		},
	}

	mid, errc1 := Pipe(ctx, src, s1)
	out, errc2 := Pipe(ctx, mid, s2)

	var results []string
	for v := range out {
		results = append(results, v)
	}
	select {
	case err := <-errc1:
		t.Fatal(err)
	default:
	}
	select {
	case err := <-errc2:
		t.Fatal(err)
	default:
	}

	sort.Strings(results)
	expected := []string{"len=2", "len=5", "len=5"}
	if len(results) != 3 {
		t.Fatalf("results = %v", results)
	}
	for i, v := range expected {
		if results[i] != v {
			t.Fatalf("results[%d] = %q, want %q", i, results[i], v)
		}
	}
}

func TestMerge(t *testing.T) {
	ctx := context.Background()
	ch1 := Source(ctx, []int{1, 2, 3})
	ch2 := Source(ctx, []int{4, 5, 6})
	ch3 := Source(ctx, []int{7, 8, 9})

	merged := Merge(ctx, ch1, ch2, ch3)

	var results []int
	for v := range merged {
		results = append(results, v)
	}
	sort.Ints(results)
	if len(results) != 9 {
		t.Fatalf("merged len = %d, want 9", len(results))
	}
	for i, v := range results {
		if v != i+1 {
			t.Fatalf("results[%d] = %d, want %d", i, v, i+1)
		}
	}
}

func TestSplit(t *testing.T) {
	ctx := context.Background()
	src := Source(ctx, []int{1, 2, 3, 4, 5, 6})

	outs := Split(ctx, src, 3)
	if len(outs) != 3 {
		t.Fatalf("split outputs = %d, want 3", len(outs))
	}

	var mu sync.Mutex
	var all []int
	var wg sync.WaitGroup
	for _, ch := range outs {
		wg.Add(1)
		go func(c <-chan int) {
			defer wg.Done()
			for v := range c {
				mu.Lock()
				all = append(all, v)
				mu.Unlock()
			}
		}(ch)
	}
	wg.Wait()

	sort.Ints(all)
	if len(all) != 6 {
		t.Fatalf("split total = %d, want 6", len(all))
	}
	for i, v := range all {
		if v != i+1 {
			t.Fatalf("all[%d] = %d, want %d", i, v, i+1)
		}
	}
}

func TestConcurrent(t *testing.T) {
	ctx := context.Background()
	n := 1000
	items := make([]int, n)
	for i := range items {
		items[i] = i
	}
	src := Source(ctx, items)

	stage := Stage[int, int]{
		Process: func(ctx context.Context, in int, emit func(int)) error {
			emit(in + 1)
			return nil
		},
		Workers: 8,
		BufSize: 50,
	}

	results, err := Run(ctx, src, stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != n {
		t.Fatalf("len = %d, want %d", len(results), n)
	}
	sort.Ints(results)
	for i, v := range results {
		if v != i+1 {
			t.Fatalf("results[%d] = %d, want %d", i, v, i+1)
		}
	}
}
