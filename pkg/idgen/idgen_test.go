package idgen_test

import (
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/idgen"
)

func TestNew_NodeRange(t *testing.T) {
	if _, err := idgen.New(-1); err == nil {
		t.Fatal("node -1 should error")
	}
	if _, err := idgen.New(1024); err == nil {
		t.Fatal("node 1024 should error")
	}
	if _, err := idgen.New(0); err != nil {
		t.Fatalf("node 0 should be ok: %v", err)
	}
	if _, err := idgen.New(1023); err != nil {
		t.Fatalf("node 1023 should be ok: %v", err)
	}
}

func TestNext_MonotonicAndPositive(t *testing.T) {
	g, _ := idgen.New(1)
	var prev int64
	for range 100000 {
		id := g.MustNext()
		if id <= 0 {
			t.Fatalf("id should be positive, got %d", id)
		}
		if id <= prev {
			t.Fatalf("id should be strictly increasing: %d <= %d", id, prev)
		}
		prev = id
	}
}

func TestNext_Unique(t *testing.T) {
	g, _ := idgen.New(7)
	seen := make(map[int64]struct{}, 200000)
	for range 200000 {
		id := g.MustNext()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id: %d", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNext_ConcurrentUnique(t *testing.T) {
	g, _ := idgen.New(3)
	const workers, per = 20, 20000
	var mu sync.Mutex
	seen := make(map[int64]struct{}, workers*per)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			local := make([]int64, 0, per)
			for range per {
				local = append(local, g.MustNext())
			}
			mu.Lock()
			for _, id := range local {
				if _, dup := seen[id]; dup {
					t.Errorf("duplicate id under concurrency: %d", id)
				}
				seen[id] = struct{}{}
			}
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(seen) != workers*per {
		t.Fatalf("want %d unique ids, got %d", workers*per, len(seen))
	}
}

func TestParse_RoundTrip(t *testing.T) {
	g, _ := idgen.New(42)
	before := time.Now().UnixMilli() - idgen.DefaultEpoch()
	id := g.MustNext()
	after := time.Now().UnixMilli() - idgen.DefaultEpoch()

	ts, node, seq := idgen.Parse(id)
	if node != 42 {
		t.Fatalf("parsed node = %d, want 42", node)
	}
	if ts < before || ts > after {
		t.Fatalf("parsed ts %d not in [%d, %d]", ts, before, after)
	}
	if seq < 0 || seq > 4095 {
		t.Fatalf("parsed seq %d out of range", seq)
	}
}

func TestTimeOf(t *testing.T) {
	g, _ := idgen.New(1)
	id := g.MustNext()
	got := idgen.TimeOf(id, idgen.DefaultEpoch())
	if d := time.Since(got); d < 0 || d > time.Second {
		t.Fatalf("TimeOf drift too large: %v", d)
	}
}

func TestNode(t *testing.T) {
	g, _ := idgen.New(511)
	if g.Node() != 511 {
		t.Fatalf("Node() = %d, want 511", g.Node())
	}
}

func TestDifferentNodesNoCollision(t *testing.T) {
	g1, _ := idgen.New(1)
	g2, _ := idgen.New(2)
	// 同一时刻两个节点各生成一批,不应冲突(node 位不同)。
	seen := make(map[int64]struct{})
	for range 10000 {
		for _, id := range []int64{g1.MustNext(), g2.MustNext()} {
			if _, dup := seen[id]; dup {
				t.Fatalf("cross-node duplicate: %d", id)
			}
			seen[id] = struct{}{}
		}
	}
}
