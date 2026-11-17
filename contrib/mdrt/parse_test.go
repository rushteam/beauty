package mdrt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSpec(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("example", "specs", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseFixture(t *testing.T) {
	for _, name := range []string{"orders.md", "pricing.md", "jobs.md"} {
		spec, diags := Parse(name, readSpec(t, name))
		if len(diags) > 0 {
			t.Fatalf("%s: %v", name, diags)
		}
		if spec.Slug != strings.TrimSuffix(name, ".md") {
			t.Fatalf("slug %s", spec.Slug)
		}
		if len(spec.Units) == 0 {
			t.Fatal("没有单元")
		}
	}
}

func TestParseErrorsHaveLines(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"no front", "hello", "frontmatter"},
		{"bad dialect", "---\ndialect: nope\nname: demo\n---\n", "方言"},
		{"name mismatch", "---\ndialect: mdrt/v0\nname: other\n---\n", "文件名"},
		{"bad h1", fm + "# 做点什么\n", "一级标题"},
		{"bad h2", fm + "# GET /x\n## 说明\n", "二级标题"},
		{"both", fm + "# GET /x\n## 行为\n算一下\n## 实现\n```js\nreturn {status:200};\n```\n## 例子\n### 正常\n```req\nGET /x\n```\n```res\n200\n```\n### 边界\n```req\nGET /x\n```\n```res\n400\n```\n", "只能写一个"},
		{"one example", fm + "# GET /x\n## 行为\n算一下\n## 例子\n### 正常\n```req\nGET /x\n```\n```res\n200\n```\n", "至少要两个"},
		{"no boundary", fm + "# GET /x\n## 行为\n算一下\n## 例子\n### 正常\n```req\nGET /x\n```\n```res\n200\n```\n### 另一个\n```req\nGET /x\n```\n```res\n201\n```\n", "边界"},
		{"bad constraint", fm + "# POST /x\n## 输入\n- n: number, 大约 1\n## 实现\n```js\nreturn {status:200};\n```\n", "约束"},
		{"bad store", fm + "# GET /x\n## 实现\n```js\nreturn {status:200};\n```\n## 例子\n### 正常\n```store\n[1]\n```\n```req\nGET /x\n```\n```res\n200\n```\n", "JSON 对象"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, diags := Parse("demo.md", tc.src)
			if len(diags) == 0 {
				t.Fatal("期望诊断")
			}
			blob := diags.Error()
			if !strings.Contains(blob, tc.want) {
				t.Fatalf("诊断 %q 不含 %q", blob, tc.want)
			}
			for _, d := range diags {
				if d.Line <= 0 {
					t.Fatalf("缺少行号: %+v", d)
				}
			}
		})
	}
}

const fm = "---\ndialect: mdrt/v0\nname: demo\nauth: none\n---\n"

func TestLowerStable(t *testing.T) {
	spec, diags := Parse("demo.md", fm+`
# GET /ping

## 实现
`+"```js"+`
return { status: 200, body: { msg: "pong" } };
`+"```"+`
`)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	js, err := Lower(spec, []unitCode{{Body: spec.Units[0].Impl.Body}})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Lower(spec, []unitCode{{Body: spec.Units[0].Impl.Body}})
	if err != nil || js != again {
		t.Fatal("下降结果不稳定")
	}
	if !strings.Contains(js, `routerAdd("GET", "/ping"`) {
		t.Fatalf("缺少路由:\n%s", js)
	}
}

func TestCheckRejectsHostAccess(t *testing.T) {
	for _, body := range []string{
		`$http.send({url:"https://x"});`,
		`eval("1");`,
		`return globalThis;`,
		`async function f(){ await 1 }`,
		"}\nrouterAdd('GET','/x',function(){})\nfunction(){",
	} {
		if err := Check(KindRoute, body); err == nil {
			t.Fatalf("应拒绝 %s", body)
		}
	}
	if err := Check(KindRoute, `return { status: 200, body: { ok: true } };`); err != nil {
		t.Fatal(err)
	}
}
