package crdt

import (
	"sort"
	"testing"
)

func TestGCounterDeltaSync(t *testing.T) {
	a := NewGCounter()
	b := NewGCounter()
	a.Inc("a", 3)
	a.Inc("a", 2) // a=5
	b.Inc("b", 7)

	// 仅交换增量。
	da := a.Delta()
	db := b.Delta()
	if a.HasDelta() || b.HasDelta() {
		t.Fatal("Delta() 后缓冲应清空")
	}
	b.MergeDelta(da)
	a.MergeDelta(db)

	if a.Value() != 12 || b.Value() != 12 {
		t.Fatalf("增量同步未收敛: a=%d b=%d", a.Value(), b.Value())
	}

	// 继续本地变更,再次只发增量。
	a.Inc("a", 1) // a=6, total 13
	b.MergeDelta(a.Delta())
	if b.Value() != 13 {
		t.Fatalf("二次增量未收敛: b=%d", b.Value())
	}
}

func TestPNCounterDeltaSync(t *testing.T) {
	a := NewPNCounter()
	b := NewPNCounter()
	a.Inc("a", 10)
	a.Dec("a", 3) // a=7
	b.Inc("b", 5)

	b.MergeDelta(a.Delta())
	a.MergeDelta(b.Delta())
	if a.Value() != 12 || b.Value() != 12 {
		t.Fatalf("PN 增量未收敛: a=%d b=%d", a.Value(), b.Value())
	}
}

func TestGSetDeltaSync(t *testing.T) {
	a := NewGSet[string]()
	b := NewGSet[string]()
	a.Add("x")
	a.Add("y")
	a.Add("x") // 重复不产生新增量
	b.Add("z")

	b.MergeDelta(a.Delta())
	a.MergeDelta(b.Delta())
	if a.Len() != 3 || b.Len() != 3 {
		t.Fatalf("GSet 增量未收敛: a=%d b=%d", a.Len(), b.Len())
	}
}

func TestORSetDeltaSync(t *testing.T) {
	a := NewORSet[string]("a")
	b := NewORSet[string]("b")
	a.Add("apple")
	a.Add("banana")
	b.MergeDelta(a.Delta())

	// b 删除 apple,增量回传给 a。
	b.Remove("apple")
	a.MergeDelta(b.Delta())

	if a.Contains("apple") {
		t.Fatal("删除未通过增量传播到 a")
	}
	if !a.Contains("banana") {
		t.Fatal("banana 不应被删")
	}
	// 收敛性:双方元素一致。
	ea, eb := a.Elements(), b.Elements()
	sort.Strings(ea)
	sort.Strings(eb)
	if len(ea) != 1 || ea[0] != "banana" || len(eb) != 1 || eb[0] != "banana" {
		t.Fatalf("ORSet 增量未收敛: a=%v b=%v", ea, eb)
	}
}

// ORSet add 优先:并发 add 与 remove,重新 add 的元素不被旧删除误删。
func TestORSetDeltaAddWins(t *testing.T) {
	a := NewORSet[string]("a")
	b := NewORSet[string]("b")
	a.Add("k")
	b.MergeDelta(a.Delta())
	// 并发:a 删除,b 重新 add(新标签)。
	da := func() *ORSet[string] { a.Remove("k"); return a.Delta() }()
	db := func() *ORSet[string] { b.Add("k"); return b.Delta() }()
	a.MergeDelta(db)
	b.MergeDelta(da)
	if !a.Contains("k") || !b.Contains("k") {
		t.Fatalf("add 应优先于并发 remove: a=%v b=%v", a.Contains("k"), b.Contains("k"))
	}
}

func TestLWWRegisterDeltaSync(t *testing.T) {
	a := NewLWWRegister[string]()
	b := NewLWWRegister[string]()
	a.Set("v1", 100, "a")
	b.MergeDelta(a.Delta())
	if v, _ := b.Get(); v != "v1" {
		t.Fatalf("b 应收到 v1, got %q", v)
	}
	// 更晚的写入胜出。
	b.Set("v2", 200, "b")
	a.MergeDelta(b.Delta())
	if v, _ := a.Get(); v != "v2" {
		t.Fatalf("a 应收敛到 v2, got %q", v)
	}
	a.Delta() // 清空 MergeDelta 带来的待传播标记
	// 更早的写入不改变已收敛的值,且不产生增量。
	a.Set("stale", 50, "a")
	if a.HasDelta() {
		t.Fatal("过期写入不应产生增量")
	}
}

// 增量同步与全量 Merge 结果一致。
func TestDeltaMatchesFullMerge(t *testing.T) {
	// 构造两个副本各自独立演进。
	full1, full2 := NewGCounter(), NewGCounter()
	dlt1, dlt2 := NewGCounter(), NewGCounter()
	for i := 0; i < 50; i++ {
		full1.Inc("n1", 1)
		dlt1.Inc("n1", 1)
	}
	for i := 0; i < 30; i++ {
		full2.Inc("n2", 1)
		dlt2.Inc("n2", 1)
	}
	// 全量路径。
	full1.Merge(full2)
	full2.Merge(full1)
	// 增量路径。
	dlt2.MergeDelta(dlt1.Delta())
	dlt1.MergeDelta(dlt2.Delta())
	if full1.Value() != dlt1.Value() || full2.Value() != dlt2.Value() {
		t.Fatalf("增量与全量不一致: full=%d/%d delta=%d/%d",
			full1.Value(), full2.Value(), dlt1.Value(), dlt2.Value())
	}
}
