package diff

import (
	"math/rand"
	"testing"
)

func TestDiffApplyRoundTrip(t *testing.T) {
	a := []int{1, 2, 3, 4, 5}
	b := []int{1, 3, 4, 6, 5, 7}
	patch := Diff(a, b)
	got, err := Apply(a, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSlice(got, b) {
		t.Fatalf("Apply=%v want %v", got, b)
	}
}

func TestDiffEmpty(t *testing.T) {
	// a 空 → 全插入
	patch := Diff([]int{}, []int{1, 2})
	if eq, ins, del := patch.Stats(); eq != 0 || ins != 2 || del != 0 {
		t.Fatalf("stats=%d,%d,%d", eq, ins, del)
	}
	// b 空 → 全删除
	patch = Diff([]int{1, 2}, []int{})
	if eq, ins, del := patch.Stats(); eq != 0 || ins != 0 || del != 2 {
		t.Fatalf("stats=%d,%d,%d", eq, ins, del)
	}
	// 两空
	patch = Diff([]int{}, []int{})
	if len(patch) != 0 {
		t.Fatalf("empty diff should be empty, got %d", len(patch))
	}
}

func TestDiffIdentical(t *testing.T) {
	a := []int{1, 2, 3}
	patch := Diff(a, a)
	eq, ins, del := patch.Stats()
	if eq != 3 || ins != 0 || del != 0 {
		t.Fatalf("identical stats=%d,%d,%d", eq, ins, del)
	}
}

func TestDiffLines(t *testing.T) {
	a := "line1\nline2\nline3"
	b := "line1\nline2-modified\nline3\nline4"
	patch := DiffLines(a, b)
	got, err := ApplyLines(a, patch)
	if err != nil {
		t.Fatal(err)
	}
	if got != b {
		t.Fatalf("ApplyLines=%q want %q", got, b)
	}
}

func TestApplyMismatch(t *testing.T) {
	a := []int{1, 2, 3}
	b := []int{1, 9, 3}
	patch := Diff(a, b)
	// 用错误的基线应用补丁应报错
	wrong := []int{5, 5, 5}
	if _, err := Apply(wrong, patch); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestFormat(t *testing.T) {
	patch := DiffLines("a\nb", "a\nc")
	out := Format(patch)
	want := " a\n-b\n+c\n"
	if out != want {
		t.Fatalf("Format=%q want %q", out, want)
	}
	compact := FormatCompact(patch)
	if compact != "-b\n+c\n" {
		t.Fatalf("FormatCompact=%q", compact)
	}
}

// 随机对拍:任意 a、b,Diff 后 Apply(a) 必等于 b。
func TestFuzzRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 2000; iter++ {
		a := randSlice(rng)
		b := randSlice(rng)
		patch := Diff(a, b)
		got, err := Apply(a, patch)
		if err != nil {
			t.Fatalf("iter %d apply error: %v", iter, err)
		}
		if !equalSlice(got, b) {
			t.Fatalf("iter %d: Apply(%v, diff)=%v want %v", iter, a, got, b)
		}
	}
}

func randSlice(rng *rand.Rand) []int {
	n := rng.Intn(12)
	s := make([]int, n)
	for i := range s {
		s[i] = rng.Intn(5) // 小值域,制造更多重复与匹配
	}
	return s
}

func equalSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
