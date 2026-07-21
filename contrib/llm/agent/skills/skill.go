// Package skills 在 llm/agent 之上实现 **Agent Skills**(与 Claude Code 的 SKILL.md 同规范):
// 一个技能 = 一个目录(SKILL.md + 可选 scripts/、references/)。加载后,skills 以"渐进式披露"
// 的方式接入 agent.Runner:
//   - SystemPrompt() 只把每个技能的名字/描述/文件清单放进系统提示(不含正文);
//   - Tools() 返回三个元工具(get_skill_instructions/reference/script),模型命中任务时才按需拉全文、
//     读引用、读/跑脚本。
//
// 这样底座仍是普通 function calling(agent.Tool + Runner),技能只是"目录 + 三个工具 + 一段目录快照"。
// 纯标准库,零外部依赖。
//
// 边界(机制而非策略):技能内容、给模型哪些技能、要不要允许执行脚本都是 policy。脚本执行默认关闭
// (只读),需 EnableExec 显式开启;文件访问带路径穿越防护。
package skills

import (
	"fmt"
	"regexp"
	"strings"
)

// Skill 是一个技能包。Instructions 是 SKILL.md 正文;Scripts/References 是子目录里的文件名清单
// (按名索引,读取时再做路径校验)。
type Skill struct {
	Name         string
	Description  string
	Instructions string
	Scripts      []string
	References   []string
	SourcePath   string
	License      string
	AllowedTools []string
	Metadata     map[string]string
}

// name 规范:小写字母/数字,连字符分隔(与 Agent Skills 规范一致)。
var nameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const (
	maxNameLen = 64
	maxDescLen = 1024
)

// parseSkillMD 解析 SKILL.md:提取 frontmatter(--- 之间)与正文。frontmatter 用最小 YAML 子集
// (key: value、内联/多行列表、metadata 嵌套一层),不引 YAML 库。
func parseSkillMD(data []byte) (fm frontmatter, body string, err error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\ufeff") // 去 BOM

	if !strings.HasPrefix(strings.TrimLeft(text, "\n"), "---") {
		return fm, "", fmt.Errorf("skills: 缺少 frontmatter(SKILL.md 需以 --- 开头)")
	}
	text = strings.TrimLeft(text, "\n")
	rest := strings.TrimPrefix(text, "---")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm, "", fmt.Errorf("skills: frontmatter 未闭合(缺少结尾 ---)")
	}
	front := rest[:end]
	after := rest[end+len("\n---"):] // 结尾 --- 之后:可能还有本行残余(多余的 - / 空格),跳到下一行
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		body = after[nl+1:]
	}
	fm = parseFrontmatter(front)
	return fm, strings.TrimLeft(body, "\n"), nil
}

type frontmatter struct {
	Name         string
	Description  string
	License      string
	AllowedTools []string
	Metadata     map[string]string
}

// parseFrontmatter 解析 frontmatter 的 YAML 子集。支持:
//   - key: value(标量,去引号)
//   - key: [a, b](内联列表)
//   - key: 后跟缩进 "- item"(多行列表)
//   - metadata: 后跟缩进 "subkey: value"(一层嵌套)
//
// 未识别的顶层标量键并入 Metadata。刻意宽松:看不懂的行忽略,不报错。
func parseFrontmatter(s string) frontmatter {
	fm := frontmatter{Metadata: map[string]string{}}
	lines := strings.Split(s, "\n")
	var listKey string  // 当前正在收集多行列表的键(顶层)
	inMetadata := false // 是否在 metadata: 嵌套块内
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indented := raw[0] == ' ' || raw[0] == '\t'
		line := strings.TrimSpace(raw)

		// 缩进的列表项 "- x"
		if indented && strings.HasPrefix(line, "- ") {
			if listKey != "" {
				assignList(&fm, listKey, []string{unquote(strings.TrimSpace(line[2:]))}, true)
			}
			continue
		}
		// 缩进的 "subkey: value"(仅在 metadata 块内收集)
		if indented && inMetadata {
			if k, v, ok := splitKV(line); ok {
				fm.Metadata[k] = unquote(v)
			}
			continue
		}
		if indented {
			continue
		}

		// 顶层 key: value
		listKey, inMetadata = "", false
		k, v, ok := splitKV(line)
		if !ok {
			continue
		}
		k = strings.ToLower(k)
		switch {
		case v == "":
			// 后续可能是多行列表或嵌套块
			if k == "metadata" {
				inMetadata = true
			} else {
				listKey = k
			}
		case strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"):
			assignList(&fm, k, parseInlineList(v), false)
		default:
			assignScalar(&fm, k, unquote(v))
		}
	}
	return fm
}

func splitKV(line string) (key, val string, ok bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

func assignScalar(fm *frontmatter, key, val string) {
	switch key {
	case "name":
		fm.Name = val
	case "description":
		fm.Description = val
	case "license":
		fm.License = val
	case "allowed-tools", "allowed_tools":
		fm.AllowedTools = parseInlineList(val) // 兼容标量写法(单个工具)
		if len(fm.AllowedTools) == 0 && val != "" {
			fm.AllowedTools = []string{val}
		}
	default:
		fm.Metadata[key] = val
	}
}

func assignList(fm *frontmatter, key string, items []string, appendMode bool) {
	if key == "allowed-tools" || key == "allowed_tools" {
		if appendMode {
			fm.AllowedTools = append(fm.AllowedTools, items...)
		} else {
			fm.AllowedTools = items
		}
		return
	}
	// 其它列表并入 metadata(逗号连接),保持信息不丢。
	if appendMode && fm.Metadata[key] != "" {
		fm.Metadata[key] += "," + strings.Join(items, ",")
	} else {
		fm.Metadata[key] = strings.Join(items, ",")
	}
}

func parseInlineList(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := unquote(strings.TrimSpace(p)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// validate 按 Agent Skills 规范校验必填与格式。
func (fm frontmatter) validate() error {
	if fm.Name == "" {
		return fmt.Errorf("skills: 缺少 name")
	}
	if len(fm.Name) > maxNameLen {
		return fmt.Errorf("skills: name %q 超过 %d 字符", fm.Name, maxNameLen)
	}
	if !nameRe.MatchString(fm.Name) {
		return fmt.Errorf("skills: name %q 非法(仅小写字母/数字/连字符)", fm.Name)
	}
	if fm.Description == "" {
		return fmt.Errorf("skills: 技能 %q 缺少 description", fm.Name)
	}
	if len(fm.Description) > maxDescLen {
		return fmt.Errorf("skills: 技能 %q 的 description 超过 %d 字符", fm.Name, maxDescLen)
	}
	return nil
}
