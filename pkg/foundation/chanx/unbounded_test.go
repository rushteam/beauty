package chanx

import (
	"sync"
	"testing"
)

func TestUnbounded_SendNeverBlocks(t *testing.T) {
	u := NewUnbounded[int]()
	// 不消费的情况下连续发送大量值，不应阻塞
	for i := range 1000 {
		u.In() <- i
	}
	u.Close()

	got := 0
	want := 0
	for v := range u.Out() {
		if v != want {
			t.Fatalf("out of order: want %d got %d", want, v)
		}
		want++
		got++
	}
	if got != 1000 {
		t.Fatalf("want 1000 values, got %d", got)
	}
}

func TestUnbounded_CloseDrainsBuffer(t *testing.T) {
	u := NewUnbounded[string]()
	u.In() <- "a"
	u.In() <- "b"
	u.Close()

	var out []string
	for v := range u.Out() {
		out = append(out, v)
	}
	if len(out) != 2 || out[0] != "a" || out[1] != "b" {
		t.Fatalf("want [a b], got %v", out)
	}
}

func TestUnbounded_ConcurrentProducers(t *testing.T) {
	u := NewUnbounded[int]()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for j := range 100 {
				u.In() <- j
			}
		})
	}
	go func() { wg.Wait(); u.Close() }()

	n := 0
	for range u.Out() {
		n++
	}
	if n != 1000 {
		t.Fatalf("want 1000, got %d", n)
	}
}
