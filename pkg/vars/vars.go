// Package vars 提供轻量的 ${KEY} 变量插值，零依赖。
// 支持默认值语法 ${KEY:-default}（仿 shell）：变量缺失时用默认值。
package vars

import (
	"regexp"
	"strings"
)

var re = regexp.MustCompile(`\$\{([^}]+)\}`)

// Render 用 vars 替换 s 中的 ${KEY} / ${KEY:-default}。
// 变量缺失且无默认值时替换为空串。
func Render(s string, vars map[string]string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return RenderFunc(s, func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	})
}

// RenderBytes 是 Render 的 []byte 版本，便于处理 JSON/模板等字节内容。
func RenderBytes(b []byte, vars map[string]string) []byte {
	if !strings.Contains(string(b), "${") {
		return b
	}
	return []byte(Render(string(b), vars))
}

// RenderFunc 用自定义解析函数替换 ${KEY} / ${KEY:-default}。
// resolve 返回 (值, 是否存在)；不存在且无默认值时替换为空串。
func RenderFunc(s string, resolve func(key string) (string, bool)) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-1]
		key, def, hasDef := strings.Cut(inner, ":-")
		key = strings.TrimSpace(key)
		if v, ok := resolve(key); ok {
			return v
		}
		if hasDef {
			return def
		}
		return ""
	})
}
