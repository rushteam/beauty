package prompt

import (
	"bytes"
	"strings"
	"text/template"

	"github.com/rushteam/beauty/contrib/llm"
)

// Template 是可复用的 prompt 模板,底层使用 Go text/template。
// 支持 {{.Variable}} 变量替换和控制流(if/range),渲染结果可作为 Slot 内容。
//
// 两种用法:
//
//  1. 一次性渲染为字符串,配合 SystemSlot 使用:
//     prompt.SystemSlot("persona", 0, tmpl.MustRender(data))
//
//  2. 创建模板驱动的动态 Slot,每次 Build 时渲染:
//     tmpl.ToSystemSlot("persona", 0, func(ctx prompt.Context) any { return data })
type Template struct {
	tmpl *template.Template
}

// Parse 从 Go 模板字符串创建 Template。
func Parse(name, pattern string) (*Template, error) {
	t, err := template.New(name).Parse(pattern)
	if err != nil {
		return nil, err
	}
	return &Template{tmpl: t}, nil
}

// MustParse 同 Parse,解析失败时 panic。适合包级变量初始化。
func MustParse(name, pattern string) *Template {
	t, err := Parse(name, pattern)
	if err != nil {
		panic("prompt: template parse: " + err.Error())
	}
	return t
}

// Render 用 data 渲染模板。
func (t *Template) Render(data any) (string, error) {
	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// MustRender 同 Render,失败时 panic。
func (t *Template) MustRender(data any) string {
	s, err := t.Render(data)
	if err != nil {
		panic("prompt: template render: " + err.Error())
	}
	return s
}

// ToSystemSlot 创建一个模板驱动的 System slot。
// dataFn 在每次 Build 时调用,返回传给模板的数据;渲染失败则该 slot 产出空内容(被跳过)。
func (t *Template) ToSystemSlot(id string, priority int, dataFn func(Context) any) Slot {
	return SystemSlot(id, priority, "").Dynamic(func(ctx Context) string {
		s, _ := t.Render(dataFn(ctx))
		return s
	})
}

// ToBeforeSlot 创建一个模板驱动的 Before slot。
func (t *Template) ToBeforeSlot(id string, role llm.Role, priority int, dataFn func(Context) any) Slot {
	return BeforeSlot(id, role, priority, "").Dynamic(func(ctx Context) string {
		s, _ := t.Render(dataFn(ctx))
		return s
	})
}

// ToAfterSlot 创建一个模板驱动的 After slot。
func (t *Template) ToAfterSlot(id string, role llm.Role, priority int, dataFn func(Context) any) Slot {
	return AfterSlot(id, role, priority, "").Dynamic(func(ctx Context) string {
		s, _ := t.Render(dataFn(ctx))
		return s
	})
}

// ToChatSlot 创建一个模板驱动的 Chat slot。
func (t *Template) ToChatSlot(id string, role llm.Role, depth, priority int, dataFn func(Context) any) Slot {
	return ChatSlot(id, role, depth, priority, "").Dynamic(func(ctx Context) string {
		s, _ := t.Render(dataFn(ctx))
		return s
	})
}
