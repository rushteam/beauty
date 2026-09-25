package doctor

import (
	"slices"
	"testing"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.26", "1.26", 0},
		{"1.26.2", "1.26", 1},
		{"1.25.9", "1.26", -1},
		{"go1.26rc1", "1.26", 0},
		{"v1.2.3", "1.10.0", -1},
		{"1.26.0", "1.26", 0},
	}
	for _, c := range cases {
		a := c.a
		if len(a) > 2 && a[:2] == "go" {
			a = a[2:]
		}
		if got := CompareVersion(a, c.b); got != c.want {
			t.Errorf("CompareVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseGoMod(t *testing.T) {
	m := ParseGoMod([]byte(`module example.com/svc // 注释

go 1.26.0

require github.com/rushteam/beauty v0.9.6

require (
	github.com/rushteam/beauty/contrib/gorm v0.9.6
	github.com/rushteam/beauty/contrib/codec/kitex v0.9.6 // indirect
	gorm.io/gorm v1.30.0
)

replace github.com/rushteam/beauty => ../beauty

replace (
	github.com/rushteam/beauty/contrib/gorm v0.9.6 => github.com/fork/gorm v0.1.0
)
`))
	if m.Module != "example.com/svc" || m.Go != "1.26.0" {
		t.Fatalf("module/go = %q/%q", m.Module, m.Go)
	}
	if m.Requires["github.com/rushteam/beauty"] != "v0.9.6" {
		t.Errorf("beauty require = %q", m.Requires["github.com/rushteam/beauty"])
	}
	if got, want := m.ContribDeps(), []string{"codec/kitex", "gorm"}; !slices.Equal(got, want) {
		t.Errorf("ContribDeps = %v, want %v", got, want)
	}
	if m.Replaces["github.com/rushteam/beauty"] != "../beauty" {
		t.Errorf("replace = %q", m.Replaces["github.com/rushteam/beauty"])
	}

	var replaceWarn bool
	for _, r := range checkGoMod(m) {
		if r.Name == "replace" {
			replaceWarn = true
			if r.Level != Warn {
				t.Errorf("replace level = %v", r.Level)
			}
		}
	}
	if !replaceWarn {
		t.Error("本地 replace 未产生警告")
	}
}
