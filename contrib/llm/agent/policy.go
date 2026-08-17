package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// PolicyMode 控制未匹配规则时的默认行为。
type PolicyMode int

const (
	// PolicyDefault 未匹配则放行(Deny/Ask 规则仍生效)。
	PolicyDefault PolicyMode = iota
	// PolicyPlan 未匹配(且不在 Allow 内)则 Ask,接到现有 HITL。
	PolicyPlan
	// PolicyBypass 忽略 Ask 规则,仅 Deny 生效。
	PolicyBypass
	// PolicyAuto 等同 Default:未匹配放行。
	PolicyAuto
)

// PolicyRule 是一条工具准入规则。Pattern 空表示整工具;
// 非空时按 glob 匹配参数(常见字段 command/path,以及原始 JSON)。
// 写法与 Claude Code 对齐: `Bash`、`Bash(*)`、`Bash(npm:*)`。
type PolicyRule struct {
	ToolName string
	Pattern  string
}

// ToolPolicy 是注册层/执行层的工具准入策略。
//
// 判定顺序:Deny > Ask > Allow 白名单 > Mode 默认。
// Allow 非空时为硬白名单:未命中 Allow 的调用视为 Deny(`--approve`/Bypass 不能绕过 Deny)。
// 子 Agent 应继承同一 *ToolPolicy(传同一指针)。
type ToolPolicy struct {
	Mode  PolicyMode
	Allow []PolicyRule
	Deny  []PolicyRule
	Ask   []PolicyRule
}

// ParseRule 解析 "Bash" / "Bash(*)" / "Bash(npm:*)" 形式的规则。大小写不敏感工具名。
func ParseRule(s string) PolicyRule {
	s = strings.TrimSpace(s)
	if s == "" {
		return PolicyRule{}
	}
	name := s
	pat := ""
	if i := strings.IndexByte(s, '('); i >= 0 && strings.HasSuffix(s, ")") {
		name = s[:i]
		pat = s[i+1 : len(s)-1]
	}
	return PolicyRule{
		ToolName: strings.ToLower(strings.TrimSpace(name)),
		Pattern:  strings.TrimSpace(pat),
	}
}

// ParseRules 批量解析规则字符串。
func ParseRules(ss ...string) []PolicyRule {
	out := make([]PolicyRule, 0, len(ss))
	for _, s := range ss {
		r := ParseRule(s)
		if r.ToolName == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Decide 对一次工具调用给出 Permission。
func (p *ToolPolicy) Decide(tc llm.ToolCall) Permission {
	if p == nil {
		return PermitAllow
	}
	if matchRules(p.Deny, tc) {
		return PermitDeny
	}
	ask := matchRules(p.Ask, tc)
	allow := matchRules(p.Allow, tc)
	hasAllow := len(p.Allow) > 0

	if p.Mode == PolicyBypass {
		if hasAllow && !allow {
			return PermitDeny
		}
		return PermitAllow
	}
	if ask {
		return PermitAsk
	}
	if hasAllow {
		if allow {
			return PermitAllow
		}
		return PermitDeny
	}
	if p.Mode == PolicyPlan {
		return PermitAsk
	}
	return PermitAllow
}

// Filter 从广告给模型的工具集中去掉整工具 Deny、以及白名单外的工具。
// 带参数的规则(如 Bash(npm:*))仍会保留该工具名,由 Decide 在执行时拦截。
func (p *ToolPolicy) Filter(tools []Tool) []Tool {
	if p == nil || (len(p.Allow) == 0 && len(p.Deny) == 0) {
		return tools
	}
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if p.hideTool(t.Def.Name) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (p *ToolPolicy) hideTool(name string) bool {
	n := strings.ToLower(name)
	if hasToolWide(p.Deny, n) {
		return true
	}
	if len(p.Allow) == 0 {
		return false
	}
	for _, r := range p.Allow {
		if r.ToolName == n {
			return false
		}
	}
	return true
}

// AsScope 把策略做成 ToolScope(只影响广告给模型的工具集)。
func (p *ToolPolicy) AsScope() ToolScope {
	return ToolScopeFunc(func(_ context.Context, _ int, tools []Tool) []Tool {
		return p.Filter(tools)
	})
}

func hasToolWide(rules []PolicyRule, name string) bool {
	for _, r := range rules {
		if r.ToolName == name && (r.Pattern == "" || r.Pattern == "*") {
			return true
		}
	}
	return false
}

func matchRules(rules []PolicyRule, tc llm.ToolCall) bool {
	name := strings.ToLower(tc.Name)
	for _, r := range rules {
		if r.ToolName != name {
			continue
		}
		if matchPattern(r.Pattern, tc) {
			return true
		}
	}
	return false
}

func matchPattern(pat string, tc llm.ToolCall) bool {
	if pat == "" || pat == "*" {
		return true
	}
	pat = strings.ReplaceAll(pat, ":*", "*")
	for _, s := range matchTexts(tc) {
		if globMatch(pat, s) {
			return true
		}
	}
	return false
}

func matchTexts(tc llm.ToolCall) []string {
	out := make([]string, 0, 4)
	raw := strings.TrimSpace(string(tc.Arguments))
	if raw != "" {
		out = append(out, raw)
	}
	var obj map[string]any
	if len(tc.Arguments) > 0 && json.Unmarshal(tc.Arguments, &obj) == nil {
		for _, k := range []string{"command", "path", "query", "url", "file_path"} {
			if v, ok := obj[k]; ok {
				if s, ok := v.(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// globMatch 支持 * (任意序列) 与 ? (单字符);大小写不敏感。
func globMatch(pat, s string) bool {
	pat = strings.ToLower(pat)
	s = strings.ToLower(s)
	return globMatchAt(pat, s)
}

func globMatchAt(pat, s string) bool {
	for {
		if pat == "" {
			return s == ""
		}
		if pat[0] == '*' {
			pat = pat[1:]
			if pat == "" {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if globMatchAt(pat, s[i:]) {
					return true
				}
			}
			return false
		}
		if s == "" {
			return false
		}
		if pat[0] != '?' && pat[0] != s[0] {
			return false
		}
		pat = pat[1:]
		s = s[1:]
	}
}

func stricterPerm(a, b Permission) Permission {
	if a > b {
		return a
	}
	return b
}
