package loadbalance_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/loadbalance"
)

// testNode 测试用节点。
type testNode struct {
	id     string
	weight int
}

func (n testNode) ID() string  { return n.id }
func (n testNode) Weight() int { return n.weight }

func nodes() []testNode {
	return []testNode{
		{id: "a", weight: 1},
		{id: "b", weight: 2},
		{id: "c", weight: 3},
	}
}

// ===== ConsistentHash =====

func TestConsistentHash_StableRouting(t *testing.T) {
	ch := loadbalance.NewConsistentHash(nodes(), loadbalance.WithVirtualFactor[testNode](50))
	first, ok := ch.Get("user:alice")
	if !ok {
		t.Fatal("want a node")
	}
	// 同一 key 多次查询应返回同一节点。
	for range 10 {
		got, ok := ch.Get("user:alice")
		if !ok || got.ID() != first.ID() {
			t.Fatalf("routing not stable: want %s, got %v ok=%v", first.ID(), got, ok)
		}
	}
}

func TestConsistentHash_DifferentKeysDistribute(t *testing.T) {
	ch := loadbalance.NewConsistentHash(nodes(), loadbalance.WithVirtualFactor[testNode](200))
	hits := map[string]int{}
	for i := range 3000 {
		key := fmt.Sprintf("key-%d", i)
		got, ok := ch.Get(key)
		if !ok {
			t.Fatal("want a node")
		}
		hits[got.ID()]++
	}
	// 三个节点都应命中(虚拟节点足够多)。
	if len(hits) != 3 {
		t.Fatalf("want 3 nodes hit, got %d: %v", len(hits), hits)
	}
}

func TestConsistentHash_WeightedDistribution(t *testing.T) {
	// weight: a=1 b=2 c=3,总权重 6。启用 weighted,期望 c 的流量约 a 的 3 倍。
	ch := loadbalance.NewConsistentHash(nodes(), loadbalance.WithVirtualFactor[testNode](200))
	hits := map[string]int{}
	const n = 60000
	for i := range n {
		got, _ := ch.Get(fmt.Sprintf("k-%d", i))
		hits[got.ID()]++
	}
	// 容差 15%,虚拟节点随机性较大。
	if !approx(hits["c"], n/2, n/10) { // c 期望约 50%(3/6)
		t.Errorf("c want ~%d, got %d", n/2, hits["c"])
	}
	if !approx(hits["a"], n/6, n/10) { // a 期望约 1/6
		t.Errorf("a want ~%d, got %d", n/6, hits["a"])
	}
}

func TestConsistentHash_Unweighted(t *testing.T) {
	// 关闭 weighted:三节点虚拟节点数相同,流量应大致均匀。
	ch := loadbalance.NewConsistentHash(nodes(),
		loadbalance.WithWeighted[testNode](false),
		loadbalance.WithVirtualFactor[testNode](200),
	)
	hits := map[string]int{}
	const n = 60000
	for i := range n {
		got, _ := ch.Get(fmt.Sprintf("k-%d", i))
		hits[got.ID()]++
	}
	// 期望各约 1/3,容差 10%。
	for _, id := range []string{"a", "b", "c"} {
		if !approx(hits[id], n/3, n/10) {
			t.Errorf("%s want ~%d, got %d", id, n/3, hits[id])
		}
	}
}

func TestConsistentHash_Replicas(t *testing.T) {
	ch := loadbalance.NewConsistentHash(nodes(),
		loadbalance.WithVirtualFactor[testNode](50),
		loadbalance.WithReplica[testNode](3),
	)
	// GetReplicas 返回主 + 不重复后续。
	reps := ch.GetReplicas("user:bob", 3)
	if len(reps) != 3 {
		t.Fatalf("want 3 replicas, got %d", len(reps))
	}
	seen := map[string]struct{}{}
	for _, r := range reps {
		if _, dup := seen[r.ID()]; dup {
			t.Fatalf("duplicate replica: %s", r.ID())
		}
		seen[r.ID()] = struct{}{}
	}
	// 超过节点数时应截断为节点数。
	reps = ch.GetReplicas("user:bob", 10)
	if len(reps) != 3 {
		t.Fatalf("want 3 (capped), got %d", len(reps))
	}
}

func TestConsistentHash_Empty(t *testing.T) {
	ch := loadbalance.NewConsistentHash[testNode](nil)
	if _, ok := ch.Get("k"); ok {
		t.Fatal("empty balancer should return false")
	}
	if reps := ch.GetReplicas("k", 3); reps != nil {
		t.Fatalf("empty balancer should return nil, got %v", reps)
	}
}

func TestConsistentHash_EmptyKey(t *testing.T) {
	ch := loadbalance.NewConsistentHash(nodes())
	if _, ok := ch.Get(""); ok {
		t.Fatal("empty key should return false")
	}
}

func TestConsistentHash_Nodes(t *testing.T) {
	ns := nodes()
	ch := loadbalance.NewConsistentHash(ns)
	got := ch.Nodes()
	if len(got) != len(ns) {
		t.Fatalf("want %d nodes, got %d", len(ns), len(got))
	}
}

// ===== WeightedRoundRobin =====

func TestWRR_WeightDistribution(t *testing.T) {
	// weight: a=1 b=2 c=3,一轮共 6 次。
	w := loadbalance.NewWeightedRoundRobin(nodes())
	hits := map[string]int{}
	const rounds = 1000
	for range 6 * rounds {
		got, ok := w.Next()
		if !ok {
			t.Fatal("want a node")
		}
		hits[got.ID()]++
	}
	// SWRR 每轮(6 次)精确分配:a=1 b=2 c=3。
	want := map[string]int{"a": rounds, "b": 2 * rounds, "c": 3 * rounds}
	for id, n := range want {
		if hits[id] != n {
			t.Errorf("%s want %d, got %d", id, n, hits[id])
		}
	}
}

func TestWRR_SmoothSequence(t *testing.T) {
	// 经典 SWRR:weight a=5 b=1 c=1,序列应平滑(不连续命中低权重)。
	ns := []testNode{
		{id: "a", weight: 5},
		{id: "b", weight: 1},
		{id: "c", weight: 1},
	}
	w := loadbalance.NewWeightedRoundRobin(ns)
	// 一轮 7 次,期望 a 5 次,b、c 各 1 次,且不连续。
	hits := map[string]int{}
	var seq []string
	for range 7 {
		got, _ := w.Next()
		hits[got.ID()]++
		seq = append(seq, got.ID())
	}
	if hits["a"] != 5 || hits["b"] != 1 || hits["c"] != 1 {
		t.Fatalf("one round dist want a=5 b=1 c=1, got %v", hits)
	}
	// 检查 b/c 不连续出现(平滑性)。
	for i := 1; i < len(seq); i++ {
		if seq[i] == seq[i-1] && seq[i] != "a" {
			t.Errorf("non-a node %s appears consecutively: %v", seq[i], seq)
		}
	}
}

func TestWRR_Empty(t *testing.T) {
	w := loadbalance.NewWeightedRoundRobin[testNode](nil)
	if _, ok := w.Next(); ok {
		t.Fatal("empty wrr should return false")
	}
}

func TestWRR_Reset(t *testing.T) {
	w := loadbalance.NewWeightedRoundRobin(nodes())
	for range 3 {
		_, _ = w.Next()
	}
	w.Reset()
	// Reset 后第一轮应重新从初始状态开始。
	w2 := loadbalance.NewWeightedRoundRobin(nodes())
	var first string
	if got, ok := w.Next(); ok {
		first = got.ID()
	}
	if got, ok := w2.Next(); !ok || got.ID() != first {
		t.Errorf("after Reset, first pick want %s, got %v ok=%v", first, got, ok)
	}
}

func TestWRR_ConcurrentSafe(t *testing.T) {
	w := loadbalance.NewWeightedRoundRobin(nodes())
	// 用 ID → 索引 + atomic 计数,避免 map/sync.Map 竞态。
	idx := map[string]int{"a": 0, "b": 1, "c": 2}
	var counts [3]atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			for range 100 {
				got, ok := w.Next()
				if !ok {
					t.Errorf("want a node")
					return
				}
				counts[idx[got.ID()]].Add(1)
			}
		})
	}
	wg.Wait()
	// 100 goroutine × 100 次 = 10000 次,应按权重分配。
	total := counts[0].Load() + counts[1].Load() + counts[2].Load()
	if total != 10000 {
		t.Errorf("total picks want 10000, got %d", total)
	}
}

// approx 检查 got 是否在 want±tol 内。
func approx(got, want, tol int) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// ===== WRR Update =====

func TestWRR_Update_Rebuilds(t *testing.T) {
	w := loadbalance.NewWeightedRoundRobin(nodes())
	// 消费一轮,建立 current 状态。
	for range 6 {
		_, _ = w.Next()
	}
	// 替换为不同节点集(权重 1:1:1)。
	w.Update([]testNode{
		{id: "x", weight: 1},
		{id: "y", weight: 1},
		{id: "z", weight: 1},
	})
	got := w.Nodes()
	if len(got) != 3 || got[0].ID() != "x" {
		t.Fatalf("after Update want [x y z], got %v", got)
	}
	// 重建后应按新权重 1:1:1 均匀分布。
	hits := map[string]int{}
	for range 9 {
		n, _ := w.Next()
		hits[n.ID()]++
	}
	for _, id := range []string{"x", "y", "z"} {
		if hits[id] != 3 {
			t.Errorf("%s want 3, got %d", id, hits[id])
		}
	}
}

func TestWRR_Update_IgnoresZeroWeight(t *testing.T) {
	w := loadbalance.NewWeightedRoundRobin(nodes())
	w.Update([]testNode{
		{id: "a", weight: 0},
		{id: "b", weight: 2},
	})
	got := w.Nodes()
	if len(got) != 1 || got[0].ID() != "b" {
		t.Fatalf("want only b, got %v", got)
	}
}

// ===== RoundRobin =====

func TestRoundRobin_Distribution(t *testing.T) {
	r := loadbalance.NewRoundRobin(nodes())
	hits := map[string]int{}
	for range 9 {
		n, ok := r.Next()
		if !ok {
			t.Fatal("want a node")
		}
		hits[n.ID()]++
	}
	// 3 节点 × 3 轮 = 各 3 次。
	for _, id := range []string{"a", "b", "c"} {
		if hits[id] != 3 {
			t.Errorf("%s want 3, got %d", id, hits[id])
		}
	}
}

func TestRoundRobin_Empty(t *testing.T) {
	r := loadbalance.NewRoundRobin[testNode](nil)
	if _, ok := r.Next(); ok {
		t.Fatal("empty rr should return false")
	}
}

func TestRoundRobin_Update(t *testing.T) {
	r := loadbalance.NewRoundRobin(nodes())
	r.Update([]testNode{{id: "solo", weight: 1}})
	for range 3 {
		got, ok := r.Next()
		if !ok || got.ID() != "solo" {
			t.Fatalf("want solo, got %v ok=%v", got, ok)
		}
	}
}

func TestRoundRobin_ConcurrentSafe(t *testing.T) {
	r := loadbalance.NewRoundRobin(nodes())
	idx := map[string]int{"a": 0, "b": 1, "c": 2}
	var counts [3]atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			for range 100 {
				got, ok := r.Next()
				if !ok {
					t.Errorf("want a node")
					return
				}
				counts[idx[got.ID()]].Add(1)
			}
		})
	}
	wg.Wait()
	total := counts[0].Load() + counts[1].Load() + counts[2].Load()
	if total != 10000 {
		t.Errorf("total picks want 10000, got %d", total)
	}
}
