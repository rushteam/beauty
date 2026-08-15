package agent

import (
	"encoding/json"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
)

// ApprovalRule 是一条持久化的审批规则。
// 当工具名匹配且(Arguments 为 nil 或精确匹配时)自动放行,无需再次审批。
type ApprovalRule struct {
	ToolName  string            `json:"tool_name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// matches 检查规则是否匹配给定的工具调用。
func (r ApprovalRule) matches(tc llm.ToolCall) bool {
	if r.ToolName != tc.Name {
		return false
	}
	if r.Arguments == nil {
		return true
	}
	var args map[string]string
	if len(tc.Arguments) > 0 {
		_ = json.Unmarshal(tc.Arguments, &args)
	}
	if len(args) != len(r.Arguments) {
		return false
	}
	for k, v := range r.Arguments {
		if args[k] != v {
			return false
		}
	}
	return true
}

// ApprovalStore 管理 standing approval rules,支持并发访问。
// 可挂到 Runner 上,askRequirements 检查前先过 standing rules。
type ApprovalStore struct {
	mu    sync.RWMutex
	rules []ApprovalRule
}

// NewApprovalStore 创建空的审批规则存储。
func NewApprovalStore() *ApprovalStore {
	return &ApprovalStore{}
}

// Add 添加一条 standing rule(后续同名工具自动放行)。
func (s *ApprovalStore) Add(rule ApprovalRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rules {
		if r.ToolName == rule.ToolName && jsonEqual(r.Arguments, rule.Arguments) {
			return
		}
	}
	s.rules = append(s.rules, rule)
}

// IsApproved 检查给定工具调用是否匹配任何 standing rule。
func (s *ApprovalStore) IsApproved(tc llm.ToolCall) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.rules {
		if r.matches(tc) {
			return true
		}
	}
	return false
}

// Rules 返回当前所有规则的快照。
func (s *ApprovalStore) Rules() []ApprovalRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ApprovalRule, len(s.rules))
	copy(out, s.rules)
	return out
}

// Clear 清除所有 standing rules。
func (s *ApprovalStore) Clear() {
	s.mu.Lock()
	s.rules = nil
	s.mu.Unlock()
}

// MarshalJSON 序列化(用于 session 持久化)。
func (s *ApprovalStore) MarshalJSON() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(s.rules)
}

// UnmarshalJSON 反序列化(用于 session 恢复)。
func (s *ApprovalStore) UnmarshalJSON(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Unmarshal(data, &s.rules)
}

func jsonEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
