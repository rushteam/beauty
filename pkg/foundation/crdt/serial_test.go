package crdt

import (
	"encoding/json"
	"sort"
	"testing"
)

// 模拟跨节点同步:序列化 → 传输 → 反序列化 → Merge,结果应与直接 Merge 一致。
func TestGCounterSyncViaJSON(t *testing.T) {
	a := NewGCounter()
	a.Inc("n1", 5)
	b := NewGCounter()
	b.Inc("n2", 3)

	wire, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	recv := NewGCounter()
	if err := json.Unmarshal(wire, recv); err != nil {
		t.Fatal(err)
	}
	b.Merge(recv)
	if b.Value() != 8 {
		t.Fatalf("after sync value=%d want 8", b.Value())
	}
}

func TestPNCounterJSON(t *testing.T) {
	c := NewPNCounter()
	c.Inc("x", 10)
	c.Dec("x", 4)
	wire, _ := json.Marshal(c)
	got := NewPNCounter()
	if err := json.Unmarshal(wire, got); err != nil {
		t.Fatal(err)
	}
	if got.Value() != 6 {
		t.Fatalf("value=%d want 6", got.Value())
	}
}

func TestGSetJSON(t *testing.T) {
	s := NewGSet[int]()
	s.Add(1)
	s.Add(2)
	s.Add(3)
	wire, _ := json.Marshal(s)
	got := NewGSet[int]()
	if err := json.Unmarshal(wire, got); err != nil {
		t.Fatal(err)
	}
	if got.Len() != 3 || !got.Contains(2) {
		t.Fatalf("gset json roundtrip failed: %v", got.Elements())
	}
}

func TestORSetSyncViaJSON(t *testing.T) {
	a := NewORSet[string]("a")
	a.Add("x")
	a.Add("y")
	a.Remove("x")

	wire, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	recv := NewORSet[string]("tmp")
	if err := json.Unmarshal(wire, recv); err != nil {
		t.Fatal(err)
	}
	b := NewORSet[string]("b")
	b.Merge(recv)
	els := b.Elements()
	sort.Strings(els)
	if len(els) != 1 || els[0] != "y" {
		t.Fatalf("after sync elements=%v want [y]", els)
	}
}

func TestLWWRegisterJSON(t *testing.T) {
	r := NewLWWRegister[string]()
	r.Set("hello", 123, "a")
	wire, _ := json.Marshal(r)
	got := NewLWWRegister[string]()
	if err := json.Unmarshal(wire, got); err != nil {
		t.Fatal(err)
	}
	v, ok := got.Get()
	if !ok || v != "hello" {
		t.Fatalf("lww json roundtrip: %q %v", v, ok)
	}
	// 反序列化后仍保留定序信息,旧写入不覆盖。
	got.Set("stale", 100, "a")
	if v, _ := got.Get(); v != "hello" {
		t.Fatalf("stale write after deserialize took effect: %q", v)
	}
}
