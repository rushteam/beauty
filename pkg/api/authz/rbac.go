package authz

import (
	"context"
	"strings"
	"sync"
)

// RBAC 是内置的基于角色的授权器(零依赖,内存策略,并发安全)。为角色授予
// (action, resource)权限;主体只要任一角色命中即放行。action/resource 支持通配:
//   - action:"*" 匹配任意,否则精确;
//   - resource:"*" 匹配任意;以 "/*" 结尾按前缀匹配(如 "article/*" 命中 "article/123");否则精确。
//
// 零值不可用,用 NewRBAC 构造。适合静态角色→权限;要 ABAC/动态策略用 contrib/casbin。
type RBAC struct {
	mu     sync.RWMutex
	grants map[string][]grant // role -> 授权列表
}

type grant struct{ action, resource string }

// NewRBAC 创建一个空的 RBAC 授权器。
func NewRBAC() *RBAC {
	return &RBAC{grants: make(map[string][]grant)}
}

// Grant 给 role 授予对 resource 执行 action 的权限(可链式)。
func (r *RBAC) Grant(role, action, resource string) *RBAC {
	r.mu.Lock()
	r.grants[role] = append(r.grants[role], grant{action: action, resource: resource})
	r.mu.Unlock()
	return r
}

// Authorize 实现 Enforcer:主体任一角色拥有匹配 (action, resource) 的授权即放行,否则 ErrDenied。
func (r *RBAC) Authorize(_ context.Context, sub Subject, action, resource string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, role := range sub.Roles {
		for _, g := range r.grants[role] {
			if matchAction(g.action, action) && matchResource(g.resource, resource) {
				return nil
			}
		}
	}
	return ErrDenied
}

func matchAction(pattern, a string) bool {
	return pattern == "*" || pattern == a
}

func matchResource(pattern, res string) bool {
	if pattern == "*" || pattern == res {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		return strings.HasPrefix(res, strings.TrimSuffix(pattern, "*")) // "article/*" → 前缀 "article/"
	}
	return false
}
