package crdt

import (
	"sort"
	"testing"
)

func TestGCounterMergeConverges(t *testing.T) {
	a := NewGCounter()
	b := NewGCounter()
	a.Inc("n1", 3)
	a.Inc("n1", 2) // n1=5
	b.Inc("n2", 4)
	b.Inc("n1", 1) // b 也看到 n1 的部分更新(并发)

	// 双向合并后两副本应收敛且等于各分量最大值之和。
	ac, bc := a.Clone(), b.Clone()
	a.Merge(bc)
	b.Merge(ac)
	if a.Value() != b.Value() {
		t.Fatalf("not converged: %d vs %d", a.Value(), b.Value())
	}
	if a.Value() != 9 { // max(n1)=5 + max(n2)=4
		t.Fatalf("value=%d want 9", a.Value())
	}
}

func TestGCounterIdempotentCommutative(t *testing.T) {
	a := NewGCounter()
	a.Inc("x", 10)
	b := a.Clone()
	a.Merge(b)
	a.Merge(b) // 幂等
	if a.Value() != 10 {
		t.Fatalf("idempotent merge value=%d", a.Value())
	}
}

func TestPNCounter(t *testing.T) {
	a := NewPNCounter()
	a.Inc("n1", 10)
	a.Dec("n1", 3)
	b := NewPNCounter()
	b.Inc("n2", 5)
	b.Dec("n2", 8)

	a.Merge(b)
	if a.Value() != 10-3+5-8 {
		t.Fatalf("pn value=%d want %d", a.Value(), 10-3+5-8)
	}
}

func TestGSet(t *testing.T) {
	a := NewGSet[string]()
	a.Add("x")
	b := NewGSet[string]()
	b.Add("y")
	a.Merge(b)
	if !a.Contains("x") || !a.Contains("y") || a.Len() != 2 {
		t.Fatalf("gset merge failed: %v", a.Elements())
	}
}

func TestORSetAddWinsOnConcurrentRemove(t *testing.T) {
	// 副本 a 删除 x,副本 b 并发重新 add x;合并后 x 应存在(add 优先)。
	a := NewORSet[string]("a")
	a.Add("x")
	b := a.Clone()
	b.node = "b"

	a.Remove("x") // a 删掉它观察到的那个标签
	b.Add("x")    // b 并发新增一个新标签

	a.Merge(b)
	b.Merge(a)
	if !a.Contains("x") || !b.Contains("x") {
		t.Fatalf("add should win: a=%v b=%v", a.Contains("x"), b.Contains("x"))
	}
}

func TestORSetRemoveConverges(t *testing.T) {
	a := NewORSet[string]("a")
	a.Add("p")
	a.Add("q")
	b := a.Clone()
	b.node = "b"

	a.Remove("p")

	a.Merge(b)
	b.Merge(a)
	ea, eb := a.Elements(), b.Elements()
	sort.Strings(ea)
	sort.Strings(eb)
	if len(ea) != 1 || ea[0] != "q" {
		t.Fatalf("a elements=%v want [q]", ea)
	}
	if len(eb) != len(ea) || eb[0] != ea[0] {
		t.Fatalf("not converged: a=%v b=%v", ea, eb)
	}
}

func TestLWWRegister(t *testing.T) {
	a := NewLWWRegister[string]()
	b := NewLWWRegister[string]()
	a.Set("hello", 100, "a")
	b.Set("world", 200, "b") // 更晚 → 胜

	a.Merge(b)
	b.Merge(a.Clone())
	va, _ := a.Get()
	vb, _ := b.Get()
	if va != "world" || vb != "world" {
		t.Fatalf("lww not converged: a=%q b=%q", va, vb)
	}

	// 时间戳相同,按 node 字典序打破平局(b > a)。
	x := NewLWWRegister[int]()
	x.Set(1, 50, "a")
	x.Set(2, 50, "b")
	if v, _ := x.Get(); v != 2 {
		t.Fatalf("tie-break failed: %d", v)
	}
	x.Set(3, 50, "a") // node a < b,不应生效
	if v, _ := x.Get(); v != 2 {
		t.Fatalf("stale write took effect: %d", v)
	}
}
