package agent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// ==== 工具参数 JSON 容错修复 ====
//
// 模型吐出的 tool_call 参数常常是"几乎合法"的 JSON:被 ```代码围栏包住、尾逗号、用单引号、
// key 没加引号、混入 JS/Python 常量(True/False/None/NaN/undefined)或注释、字符串里有裸换行。
// RepairJSON 尽力把这类近似 JSON 修成合法 JSON。它是一个薄的单遍扫描重写器(非完整的递归下降
// 解析器),覆盖上述最常见的模型笔误,纯标准库。
//
// 安全约定:RepairJSON 只有在修复结果**重新通过 json.Valid** 时才返回 ok=true;否则返回
// (nil,false),调用方应保留原始字节。因此即便修复不完美,也绝不会把更坏的输入喂给下游。
//
// 接线:Runner.RepairToolArgs=true 时,dispatch 在 tool_call 参数 json.Valid 失败时先试修复,
// 修好(且重校验通过)才把修复后的参数交给 Tool.Call。默认关闭,是 opt-in 机制而非策略。

// RepairJSON 尽力把近似 JSON 修成合法 JSON。修复结果重新通过 json.Valid 才返回 ok=true。
func RepairJSON(in []byte) (out []byte, ok bool) {
	s := stripCodeFence(string(in))
	s = extractJSONSpan(s)
	if s == "" {
		return nil, false
	}
	fixed := (&jsonRepairer{src: s}).rewrite()
	if json.Valid([]byte(fixed)) {
		return []byte(fixed), true
	}
	return nil, false
}

// stripCodeFence 去掉 ```json ... ``` / ``` ... ``` 代码围栏(取围栏内内容)。
func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	t = t[3:]
	// 去掉紧跟的语言标注行(如 json)。
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		if lang := strings.TrimSpace(t[:i]); lang == "" || isWord(lang) {
			t = t[i+1:]
		}
	}
	if i := strings.LastIndex(t, "```"); i >= 0 {
		t = t[:i]
	}
	return t
}

// extractJSONSpan 截取从首个 { 或 [ 到与之匹配的末个 } 或 ] 之间的片段,丢弃前后散文。
// 仅按最外层括号粗略配对(字符串内的括号不计),用于剥离模型在 JSON 前后附带的解释文字。
func extractJSONSpan(s string) string {
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return strings.TrimSpace(s)
	}
	var last int = -1
	depth := 0
	inStr := false
	var quote byte
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == quote {
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr, quote = true, c
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				last = i
			}
		}
	}
	if last > start {
		return s[start : last+1]
	}
	return s[start:] // 未闭合:交给重写器补齐
}

// jsonRepairer 是单遍扫描重写器,维护一个容器栈以识别 object 的 key 位置。
type jsonRepairer struct {
	src string
	i   int
	out strings.Builder
	// stack 记录当前所处容器:'{' 或 '['。
	stack []byte
	// expectKey 表示在 object 中当前处于 key 位置(刚进 { 或刚过 ,)。
	expectKey bool
}

func (r *jsonRepairer) inObject() bool {
	return len(r.stack) > 0 && r.stack[len(r.stack)-1] == '{'
}

func (r *jsonRepairer) rewrite() string {
	for r.i < len(r.src) {
		r.skipSpaceAndComments()
		if r.i >= len(r.src) {
			break
		}
		c := r.src[r.i]
		switch {
		case c == '{':
			r.out.WriteByte('{')
			r.stack = append(r.stack, '{')
			r.expectKey = true
			r.i++
		case c == '[':
			r.out.WriteByte('[')
			r.stack = append(r.stack, '[')
			r.expectKey = false
			r.i++
		case c == '}' || c == ']':
			r.trimTrailingComma()
			r.out.WriteByte(c)
			if len(r.stack) > 0 {
				r.stack = r.stack[:len(r.stack)-1]
			}
			r.expectKey = false
			r.i++
		case c == ',':
			// 尾逗号:后面(跳过空白/注释)紧跟 } 或 ] 时丢弃。
			if r.commaIsTrailing() {
				r.i++
				continue
			}
			r.out.WriteByte(',')
			r.i++
			r.expectKey = r.inObject()
		case c == ':':
			r.out.WriteByte(':')
			r.i++
			r.expectKey = false
		case c == '"' || c == '\'':
			r.writeString()
			r.expectKey = false
		default:
			if r.inObject() && r.expectKey && isIdentStart(c) {
				r.writeBareKey()
			} else {
				r.writeLiteral()
			}
			r.expectKey = false
		}
	}
	return r.out.String()
}

// skipSpaceAndComments 跳过空白与 // 行注释、/* */ 块注释(不写入输出)。
func (r *jsonRepairer) skipSpaceAndComments() {
	for r.i < len(r.src) {
		c := r.src[r.i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			r.out.WriteByte(c)
			r.i++
		case c == '/' && r.i+1 < len(r.src) && r.src[r.i+1] == '/':
			r.i += 2
			for r.i < len(r.src) && r.src[r.i] != '\n' {
				r.i++
			}
		case c == '/' && r.i+1 < len(r.src) && r.src[r.i+1] == '*':
			r.i += 2
			for r.i+1 < len(r.src) && !(r.src[r.i] == '*' && r.src[r.i+1] == '/') {
				r.i++
			}
			r.i += 2
			if r.i > len(r.src) {
				r.i = len(r.src)
			}
		default:
			return
		}
	}
}

// commaIsTrailing 从当前逗号向后看,跳过空白/注释后若为 } 或 ] 则该逗号为尾逗号。
func (r *jsonRepairer) commaIsTrailing() bool {
	j := r.i + 1
	for j < len(r.src) {
		c := r.src[j]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			j++
		case c == '/' && j+1 < len(r.src) && r.src[j+1] == '/':
			j += 2
			for j < len(r.src) && r.src[j] != '\n' {
				j++
			}
		case c == '/' && j+1 < len(r.src) && r.src[j+1] == '*':
			j += 2
			for j+1 < len(r.src) && !(r.src[j] == '*' && r.src[j+1] == '/') {
				j++
			}
			j += 2
		default:
			return c == '}' || c == ']'
		}
	}
	return true // 后面只剩空白 → 视作尾逗号丢弃
}

// trimTrailingComma 在写入 } / ] 前,去掉输出末尾已写入的尾逗号(及其后空白)。
func (r *jsonRepairer) trimTrailingComma() {
	s := r.out.String()
	end := len(s)
	j := end
	for j > 0 {
		c := s[j-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			j--
			continue
		}
		break
	}
	if j > 0 && s[j-1] == ',' {
		r.out.Reset()
		r.out.WriteString(s[:j-1])
		r.out.WriteString(s[j:end]) // 保留逗号后的空白排版
	}
}

// writeString 读取一个字符串字面量(定界符可为 " 或 '),统一以双引号输出,转义内部双引号与裸换行。
func (r *jsonRepairer) writeString() {
	quote := r.src[r.i]
	r.i++
	r.out.WriteByte('"')
	for r.i < len(r.src) {
		c := r.src[r.i]
		if c == '\\' {
			// 保留转义序列;单引号定界时的 \' 输出为字面 '。
			if r.i+1 < len(r.src) {
				nxt := r.src[r.i+1]
				if quote == '\'' && nxt == '\'' {
					r.out.WriteByte('\'')
					r.i += 2
					continue
				}
				r.out.WriteByte('\\')
				r.out.WriteByte(nxt)
				r.i += 2
				continue
			}
			r.out.WriteString("\\\\")
			r.i++
			continue
		}
		if c == quote {
			r.i++
			r.out.WriteByte('"')
			return
		}
		switch c {
		case '"':
			r.out.WriteString("\\\"") // 单引号串里出现的裸双引号需转义
		case '\n':
			r.out.WriteString("\\n")
		case '\r':
			r.out.WriteString("\\r")
		case '\t':
			r.out.WriteString("\\t")
		default:
			r.out.WriteByte(c)
		}
		r.i++
	}
	r.out.WriteByte('"') // 未闭合 → 补齐
}

// writeBareKey 读取一个未加引号的对象 key(标识符),以双引号输出。
func (r *jsonRepairer) writeBareKey() {
	start := r.i
	for r.i < len(r.src) && isIdentPart(r.src[r.i]) {
		r.i++
	}
	r.out.WriteByte('"')
	r.out.WriteString(r.src[start:r.i])
	r.out.WriteByte('"')
}

// writeLiteral 读取一个裸 token(数字/true/false/null 或需归一化的 JS/Py 常量),规范化后输出。
func (r *jsonRepairer) writeLiteral() {
	start := r.i
	for r.i < len(r.src) {
		c := r.src[r.i]
		if c == ',' || c == '}' || c == ']' || c == ':' || c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			break
		}
		r.i++
	}
	tok := r.src[start:r.i]
	switch tok {
	case "True":
		r.out.WriteString("true")
	case "False":
		r.out.WriteString("false")
	case "None", "null":
		r.out.WriteString("null")
	case "NaN", "Infinity", "-Infinity", "undefined", "":
		r.out.WriteString("null")
	default:
		r.out.WriteString(normalizeNumber(tok))
	}
}

// normalizeNumber 归一化数字:去掉前导 +、修正 .5 → 0.5、5. → 5.0;非数字原样返回。
func normalizeNumber(tok string) string {
	t := tok
	neg := ""
	t = strings.TrimPrefix(t, "+")
	if strings.HasPrefix(t, "-") {
		neg, t = "-", t[1:]
	}
	if t == "" || !isNumberish(t) {
		return tok
	}
	if strings.HasPrefix(t, ".") {
		t = "0" + t
	}
	if strings.HasSuffix(t, ".") {
		t += "0"
	}
	return neg + t
}

func isNumberish(t string) bool {
	dot := false
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && !dot:
			dot = true
		case (c == 'e' || c == 'E') && i > 0:
		case (c == '+' || c == '-') && i > 0 && (t[i-1] == 'e' || t[i-1] == 'E'):
		default:
			return false
		}
	}
	return strings.ContainsAny(t, "0123456789")
}

func isWord(s string) bool {
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return utf8.RuneCountInString(s) > 0
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-' || c == '.'
}
