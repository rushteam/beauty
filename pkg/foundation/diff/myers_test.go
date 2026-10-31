package diff

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestMyersApplyRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"", ""},
		{"a\nb\nc", "a\nb\nc"},
		{"a\nb\nc", "a\nx\nc"},
		{"", "a\nb"},
		{"a\nb", ""},
		{"the\nquick\nbrown\nfox", "the\nlazy\nbrown\ncat\nfox"},
	}
	for i, c := range cases {
		patch := DiffMyersLines(c[0], c[1])
		got, err := ApplyLines(c[0], patch)
		if err != nil {
			t.Fatalf("case %d apply: %v", i, err)
		}
		if got != c[1] {
			t.Fatalf("case %d: got %q want %q", i, got, c[1])
		}
	}
}

// Myers 与 LCS Diff 的编辑距离(插入+删除数)应相同(都最小)。
func TestMyersSameEditDistanceAsLCS(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		a := randRunes(rng, rng.Intn(30))
		b := randRunes(rng, rng.Intn(30))
		pm := DiffMyers(a, b)
		pl := Diff(a, b)
		_, im, dm := pm.Stats()
		_, il, dl := pl.Stats()
		if im+dm != il+dl {
			t.Fatalf("iter %d edit distance differ: myers=%d lcs=%d\na=%v\nb=%v", iter, im+dm, il+dl, a, b)
		}
		// 应用后一致。
		got, err := Apply(a, pm)
		if err != nil || !reflect.DeepEqual(got, b) {
			t.Fatalf("iter %d myers apply mismatch: %v", iter, err)
		}
	}
}

func randRunes(rng *rand.Rand, n int) []rune {
	out := make([]rune, n)
	for i := range out {
		out[i] = rune('a' + rng.Intn(4)) // 小字母表,制造更多重复/匹配
	}
	return out
}
