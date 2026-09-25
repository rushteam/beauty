package filter

import (
	"fmt"
	"testing"
)

func TestBloomNoFalseNegative(t *testing.T) {
	f := NewBloom(1000, 0.01)
	keys := make([]string, 1000)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
		f.AddString(keys[i])
	}
	// 已插入的绝不能漏报(布隆无假阴性)
	for _, k := range keys {
		if !f.TestString(k) {
			t.Fatalf("false negative for %q", k)
		}
	}
	if f.Len() != 1000 {
		t.Fatalf("Len=%d, want 1000", f.Len())
	}
}

func TestBloomFalsePositiveRate(t *testing.T) {
	n := 10000
	f := NewBloom(n, 0.01)
	for i := 0; i < n; i++ {
		f.AddString(fmt.Sprintf("in-%d", i))
	}
	// 测试未插入元素的假阳性比例应接近目标 1%
	fp := 0
	trials := 10000
	for i := 0; i < trials; i++ {
		if f.TestString(fmt.Sprintf("out-%d", i)) {
			fp++
		}
	}
	rate := float64(fp) / float64(trials)
	if rate > 0.03 { // 留足余量,只验证量级
		t.Fatalf("false positive rate too high: %.4f (fill=%.3f)", rate, f.FillRatio())
	}
}

func TestBloomTestAndAdd(t *testing.T) {
	f := NewBloom(100, 0.01)
	if f.TestAndAdd([]byte("a")) {
		t.Fatal("first insert should report not existed")
	}
	if !f.TestAndAdd([]byte("a")) {
		t.Fatal("second insert should report existed")
	}
}

func TestBloomReset(t *testing.T) {
	f := NewBloom(100, 0.01)
	f.AddString("x")
	f.Reset()
	if f.TestString("x") {
		t.Fatal("after reset should not contain x")
	}
	if f.Len() != 0 {
		t.Fatal("after reset Len should be 0")
	}
}

func TestCuckooBasic(t *testing.T) {
	c := NewCuckoo(10000)
	keys := make([]string, 5000)
	for i := range keys {
		keys[i] = fmt.Sprintf("item-%d", i)
		if !c.AddString(keys[i]) {
			t.Fatalf("Add failed at %d (load=%.3f)", i, c.LoadFactor())
		}
	}
	for _, k := range keys {
		if !c.ContainsString(k) {
			t.Fatalf("false negative for %q", k)
		}
	}
	if c.Len() != 5000 {
		t.Fatalf("Len=%d, want 5000", c.Len())
	}
}

func TestCuckooDelete(t *testing.T) {
	c := NewCuckoo(1000)
	c.AddString("hello")
	c.AddString("world")
	if !c.ContainsString("hello") {
		t.Fatal("should contain hello")
	}
	if !c.DeleteString("hello") {
		t.Fatal("delete hello should succeed")
	}
	if c.ContainsString("hello") {
		t.Fatal("should not contain hello after delete")
	}
	if !c.ContainsString("world") {
		t.Fatal("world should still be present")
	}
	if c.DeleteString("nonexistent") {
		t.Fatal("deleting absent key should return false")
	}
}

func TestCuckooNoFalseNegativeWithDelete(t *testing.T) {
	c := NewCuckoo(4000)
	for i := 0; i < 1000; i++ {
		c.AddString(fmt.Sprintf("k%d", i))
	}
	// 删一半
	for i := 0; i < 500; i++ {
		if !c.DeleteString(fmt.Sprintf("k%d", i)) {
			t.Fatalf("delete k%d failed", i)
		}
	}
	// 剩下一半必须仍在(无假阴性)
	for i := 500; i < 1000; i++ {
		if !c.ContainsString(fmt.Sprintf("k%d", i)) {
			t.Fatalf("false negative for k%d after deletes", i)
		}
	}
}

func TestCuckooReset(t *testing.T) {
	c := NewCuckoo(100)
	c.AddString("a")
	c.Reset()
	if c.ContainsString("a") {
		t.Fatal("after reset should not contain a")
	}
	if c.Len() != 0 {
		t.Fatal("after reset Len should be 0")
	}
}
