package vars

import "testing"

func TestRender(t *testing.T) {
	cases := []struct {
		in   string
		vars map[string]string
		want string
	}{
		{"hello ${name}", map[string]string{"name": "bob"}, "hello bob"},
		{"${a}/${b}", map[string]string{"a": "1", "b": "2"}, "1/2"},
		{"x ${missing} y", nil, "x  y"},                                        // 缺失无默认 → 空
		{"port=${PORT:-8080}", nil, "port=8080"},                               // 默认值
		{"port=${PORT:-8080}", map[string]string{"PORT": "9090"}, "port=9090"}, // 有值覆盖默认
		{"no placeholder", map[string]string{"x": "y"}, "no placeholder"},
		{"${ spaced }", map[string]string{"spaced": "ok"}, "ok"}, // key 去空格
	}
	for _, c := range cases {
		if got := Render(c.in, c.vars); got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderBytes(t *testing.T) {
	got := RenderBytes([]byte(`{"url":"${host}/api"}`), map[string]string{"host": "h"})
	if string(got) != `{"url":"h/api"}` {
		t.Fatalf("got %s", got)
	}
}

func TestRenderFunc(t *testing.T) {
	got := RenderFunc("${a}-${b}", func(k string) (string, bool) {
		if k == "a" {
			return "X", true
		}
		return "", false
	})
	if got != "X-" {
		t.Fatalf("got %q", got)
	}
}
