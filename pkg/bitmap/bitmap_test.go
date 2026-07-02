package bitmap_test

import (
	"reflect"
	"testing"

	"github.com/rushteam/beauty/pkg/bitmap"
)

func TestSetTestClear(t *testing.T) {
	var b bitmap.Bitmap // 零值可用
	if b.Test(5) {
		t.Fatal("fresh bit should be 0")
	}
	b.Set(5)
	if !b.Test(5) {
		t.Fatal("bit 5 should be set")
	}
	b.Clear(5)
	if b.Test(5) {
		t.Fatal("bit 5 should be cleared")
	}
}

func TestAutoGrow(t *testing.T) {
	var b bitmap.Bitmap
	b.Set(1000) // 远超零值容量,应自动增长
	if !b.Test(1000) {
		t.Fatal("bit 1000 should be set after auto-grow")
	}
	if b.Count() != 1 {
		t.Fatalf("count = %d, want 1", b.Count())
	}
}

func TestNegativeAndOOB(t *testing.T) {
	var b bitmap.Bitmap
	b.Set(-1)   // 忽略
	b.Clear(-1) // 忽略
	if b.Test(-1) || b.Test(99999) {
		t.Fatal("negative/oob Test should be false")
	}
	if b.Count() != 0 {
		t.Fatal("no bits should be set")
	}
}

func TestCountAndSlice(t *testing.T) {
	b := bitmap.New(200)
	for _, i := range []int{3, 64, 65, 130, 199} {
		b.Set(i)
	}
	if b.Count() != 5 {
		t.Fatalf("count = %d, want 5", b.Count())
	}
	if got := b.Slice(); !reflect.DeepEqual(got, []int{3, 64, 65, 130, 199}) {
		t.Fatalf("slice = %v", got)
	}
}

func TestFlip(t *testing.T) {
	var b bitmap.Bitmap
	if !b.Flip(7) {
		t.Fatal("flip 0→1 should return true")
	}
	if b.Flip(7) {
		t.Fatal("flip 1→0 should return false")
	}
}

func TestAnd(t *testing.T) {
	a := bitmap.New(128)
	c := bitmap.New(128)
	for _, i := range []int{1, 2, 3, 100} {
		a.Set(i)
	}
	for _, i := range []int{2, 3, 4, 100} {
		c.Set(i)
	}
	a.And(c) // 交集 {2,3,100}
	if got := a.Slice(); !reflect.DeepEqual(got, []int{2, 3, 100}) {
		t.Fatalf("and = %v, want [2 3 100]", got)
	}
}

func TestOr(t *testing.T) {
	a := bitmap.New(64)
	c := bitmap.New(200) // 更大,验证 Or 会增长 a
	a.Set(1)
	c.Set(1)
	c.Set(150)
	a.Or(c) // {1,150}
	if got := a.Slice(); !reflect.DeepEqual(got, []int{1, 150}) {
		t.Fatalf("or = %v, want [1 150]", got)
	}
}

func TestAndNot(t *testing.T) {
	a := bitmap.New(64)
	c := bitmap.New(64)
	for _, i := range []int{1, 2, 3} {
		a.Set(i)
	}
	c.Set(2)
	a.AndNot(c) // {1,3}
	if got := a.Slice(); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("andnot = %v, want [1 3]", got)
	}
}

func TestCloneIndependent(t *testing.T) {
	a := bitmap.New(64)
	a.Set(5)
	c := a.Clone()
	c.Set(6)
	if a.Test(6) {
		t.Fatal("clone should be independent")
	}
	if !c.Test(5) {
		t.Fatal("clone should copy existing bits")
	}
}

func TestReset(t *testing.T) {
	b := bitmap.New(64)
	b.Set(1)
	b.Set(2)
	b.Reset()
	if b.Count() != 0 {
		t.Fatalf("count after reset = %d", b.Count())
	}
}

func TestConsecutiveSignIn(t *testing.T) {
	// 7 天签到,uid=42 在最后 3 天签到(第 4,5,6 天,0-indexed),第 3 天缺勤。
	days := make([]*bitmap.Bitmap, 7)
	for i := range days {
		days[i] = bitmap.New(100)
	}
	days[0].Set(42)
	days[1].Set(42)
	// days[2] 缺勤
	days[4].Set(42)
	days[5].Set(42)
	days[6].Set(42)
	if got := bitmap.ConsecutiveFromEnd(days, 42); got != 3 {
		t.Fatalf("consecutive = %d, want 3", got)
	}
	// 最后一天没签到 → 0
	days[6].Clear(42)
	if got := bitmap.ConsecutiveFromEnd(days, 42); got != 0 {
		t.Fatalf("consecutive (last day absent) = %d, want 0", got)
	}
}

func TestSignInDailyCount(t *testing.T) {
	// 当日签到人数 = Count。
	day := bitmap.New(1000)
	for _, uid := range []int{7, 42, 100, 999} {
		day.Set(uid)
	}
	if day.Count() != 4 {
		t.Fatalf("daily sign-in count = %d, want 4", day.Count())
	}
}
