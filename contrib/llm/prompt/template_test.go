package prompt

import (
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

// ---- Template 核心 ----

func TestParse_Valid(t *testing.T) {
	tmpl, err := Parse("test", "hello {{.Name}}")
	if err != nil {
		t.Fatal(err)
	}
	got, err := tmpl.Render(map[string]string{"Name": "world"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestParse_Invalid(t *testing.T) {
	_, err := Parse("bad", "{{.Foo")
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
}

func TestMustParse_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid template")
		}
	}()
	MustParse("bad", "{{.Foo")
}

func TestRender_Error(t *testing.T) {
	tmpl := MustParse("test", "{{.Method}}")
	_, err := tmpl.Render("not a struct")
	if err == nil {
		t.Fatal("expected render error")
	}
}

func TestMustRender_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for render error")
		}
	}()
	tmpl := MustParse("test", "{{.Method}}")
	tmpl.MustRender("not a struct")
}

func TestRender_TrimsWhitespace(t *testing.T) {
	tmpl := MustParse("trim", "  hello  ")
	got := tmpl.MustRender(nil)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestRender_WithRange(t *testing.T) {
	tmpl := MustParse("list", `{{range .Items}}- {{.}}
{{end}}`)
	got := tmpl.MustRender(map[string][]string{"Items": {"a", "b", "c"}})
	if !strings.Contains(got, "- a") || !strings.Contains(got, "- c") {
		t.Errorf("got %q, missing expected items", got)
	}
}

func TestRender_WithIf(t *testing.T) {
	tmpl := MustParse("cond", `{{if .Show}}visible{{end}}`)
	got := tmpl.MustRender(map[string]bool{"Show": true})
	if got != "visible" {
		t.Errorf("got %q, want %q", got, "visible")
	}
	got = tmpl.MustRender(map[string]bool{"Show": false})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ---- Template → Slot ----

func TestTemplate_ToSystemSlot(t *testing.T) {
	tmpl := MustParse("persona", "你是{{.Role}},擅长{{.Skill}}")
	slot := tmpl.ToSystemSlot("persona", 0, func(ctx Context) any {
		return map[string]string{"Role": "助手", "Skill": "编程"}
	})

	if slot.ID != "persona" {
		t.Errorf("ID = %q, want %q", slot.ID, "persona")
	}
	if slot.Position != System {
		t.Errorf("Position = %v, want System", slot.Position)
	}
	if slot.ContentFunc == nil {
		t.Fatal("ContentFunc should not be nil")
	}
	got := slot.ContentFunc(Context{})
	if got != "你是助手,擅长编程" {
		t.Errorf("got %q, want %q", got, "你是助手,擅长编程")
	}
}

func TestTemplate_ToAfterSlot(t *testing.T) {
	tmpl := MustParse("rag", "文档:{{.Doc}}")
	slot := tmpl.ToAfterSlot("rag", llm.System, 0, func(ctx Context) any {
		return map[string]string{"Doc": "内容"}
	})

	if slot.Position != After {
		t.Errorf("Position = %v, want After", slot.Position)
	}
	if slot.Role != llm.System {
		t.Errorf("Role = %q, want %q", slot.Role, llm.System)
	}
	got := slot.ContentFunc(Context{})
	if got != "文档:内容" {
		t.Errorf("got %q", got)
	}
}

func TestTemplate_ToBeforeSlot(t *testing.T) {
	tmpl := MustParse("ctx", "背景:{{.Bg}}")
	slot := tmpl.ToBeforeSlot("bg", llm.User, 0, func(ctx Context) any {
		return map[string]string{"Bg": "历史"}
	})
	if slot.Position != Before {
		t.Errorf("Position = %v, want Before", slot.Position)
	}
}

func TestTemplate_ToChatSlot(t *testing.T) {
	tmpl := MustParse("hint", "提示:{{.Msg}}")
	slot := tmpl.ToChatSlot("hint", llm.System, 1, 0, func(ctx Context) any {
		return map[string]string{"Msg": "注意"}
	})
	if slot.Position != Chat || slot.Depth != 1 {
		t.Errorf("Position=%v Depth=%d, want Chat/1", slot.Position, slot.Depth)
	}
}

func TestTemplate_SlotRenderError_ReturnsEmpty(t *testing.T) {
	tmpl := MustParse("bad", "{{.Method}}")
	slot := tmpl.ToSystemSlot("bad", 0, func(ctx Context) any {
		return "not a struct"
	})
	got := slot.ContentFunc(Context{})
	if got != "" {
		t.Errorf("render error should produce empty string, got %q", got)
	}
}

func TestTemplate_SlotInAssembler(t *testing.T) {
	tmpl := MustParse("persona", "你是{{.Role}}")
	asm := New(
		tmpl.ToSystemSlot("persona", 0, func(ctx Context) any {
			return map[string]string{"Role": "助手"}
		}),
		SystemSlot("extra", 10, "附加指令"),
	)
	out := asm.Build(Context{}, llm.Request{})
	want := "你是助手\n\n附加指令"
	if out.System != want {
		t.Errorf("System = %q, want %q", out.System, want)
	}
}

// ---- 用户自定义模板示例 ----

func TestTemplate_UserDefinedInstructPattern(t *testing.T) {
	tmpl := MustParse("instruct", `{{.Persona}}
{{if .Rules}}
Rules:
{{range .Rules}}- {{.}}
{{end}}{{end}}`)
	got := tmpl.MustRender(map[string]any{
		"Persona": "You are a helpful assistant",
		"Rules":   []string{"Be concise", "Use examples"},
	})
	if !strings.Contains(got, "You are a helpful assistant") {
		t.Error("missing persona")
	}
	if !strings.Contains(got, "- Be concise") || !strings.Contains(got, "- Use examples") {
		t.Error("missing rules")
	}
}

func TestTemplate_UserDefinedRAGPattern(t *testing.T) {
	tmpl := MustParse("rag", `{{range .Docs}}<doc source="{{.Src}}">
{{.Text}}
</doc>
{{end}}`)
	type doc struct {
		Src, Text string
	}
	got := tmpl.MustRender(map[string]any{
		"Docs": []doc{
			{Src: "api.md", Text: "API reference"},
			{Src: "guide.md", Text: "User guide"},
		},
	})
	if !strings.Contains(got, `source="api.md"`) || !strings.Contains(got, "User guide") {
		t.Errorf("unexpected: %q", got)
	}
}
